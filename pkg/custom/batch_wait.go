package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// batchWorkflowError contains only local guidance, never API prose or paths.
type batchWorkflowError struct {
	message string
	code    int
	cause   error
}

func (e *batchWorkflowError) Error() string { return e.message }
func (e *batchWorkflowError) Unwrap() error { return e.cause }
func (e *batchWorkflowError) ExitCode() int {
	if e.code != 0 {
		return e.code
	}
	return 1
}

func batchError(message string) error { return &batchWorkflowError{message: message} }

func batchContextError(err error) error {
	quiet := interruptedBatchDiagnostic(err)
	if errors.Is(err, context.Canceled) {
		code := 130
		var signalError *batchSignalError
		if errors.As(err, &signalError) {
			code = signalError.code
		}
		message := "Local batch operation interrupted. The remote batch was not canceled."
		if quiet {
			message = ""
		}
		return &batchWorkflowError{message: message, code: code, cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		message := "Local batch wait timed out. The remote batch was not canceled. Retrieve it again to continue waiting."
		if quiet {
			message = ""
		}
		return &batchWorkflowError{message: message, code: 124, cause: err}
	}
	return err
}

type batchCreateContextKey struct{}

func configureBatchCommands(root *cli.Command) {
	for _, resource := range root.Commands {
		if resource.Name != "batches" {
			continue
		}
		for _, command := range resource.Commands {
			switch command.Name {
			case "create":
				previous := command.Action
				command.Action = func(ctx context.Context, cmd *cli.Command) error {
					return previous(context.WithValue(ctx, batchCreateContextKey{}, cmd), cmd)
				}
			case "retrieve":
				command.Flags = append(command.Flags,
					&cli.BoolFlag{Name: "wait", Usage: "Wait for a terminal batch status; Ctrl+C stops local waiting only"},
					&cli.DurationFlag{Name: "poll-interval", Value: 10 * time.Second, Usage: "Delay between successful status checks with --wait"},
					&cli.DurationFlag{Name: "wait-timeout", Usage: "Total local wait duration, including requests and retries; 0 waits without a deadline"},
				)
				previous := command.Action
				command.Action = func(ctx context.Context, cmd *cli.Command) error {
					if !cmd.Bool("wait") {
						if cmd.IsSet("poll-interval") || cmd.IsSet("wait-timeout") {
							return batchError("Use --wait with --poll-interval or --wait-timeout.")
						}
						return previous(ctx, cmd)
					}
					return waitForBatch(ctx, cmd)
				}
			}
		}
		resource.Commands = append(resource.Commands, newBatchWaitCommand(), newBatchDownloadCommand())
		return
	}
}

func newBatchWaitCommand() *cli.Command {
	command := &cli.Command{
		Name: "wait", Usage: "Wait for a batch and optionally save its results",
		Description:     "Waits without submitting or canceling remote work. Use --output-dir to save available output.jsonl and error.jsonl without overwriting files.",
		HideHelpCommand: true, Suggest: true,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "batch-id", PathParam: "batch_id", Required: true},
			&cli.DurationFlag{Name: "poll-interval", Value: 10 * time.Second, Usage: "Delay between successful status checks"},
			&cli.DurationFlag{Name: "wait-timeout", Usage: "Total local duration, including downloads; 0 waits without a deadline"},
			&cli.StringFlag{Name: "output-dir", TakesFile: true, Usage: "Save available result files in this directory; create it if absent"},
		},
		Action: waitForBatch,
	}
	command.Metadata = map[string]any{"help-content": clihelp.Content{
		Description: command.Description,
		Examples:    []clihelp.Example{{Description: "Wait and save available results:", Command: "batches wait --batch-id batch_example --output-dir ./results"}},
	}}
	return command
}

// Parse once, preserving generated positional, @file, and piped input semantics.
func batchRequestOptions(ctx context.Context, cmd *cli.Command) ([]option.RequestOption, error) {
	args := cmd.Args().Slice()
	if !cmd.IsSet("batch-id") && len(args) > 0 {
		if err := cmd.Set("batch-id", args[0]); err != nil {
			return nil, err
		}
		args = args[1:]
	}
	if len(args) > 0 {
		return nil, batchError("Unexpected extra arguments. Use one batch ID.")
	}
	options, err := flagOptionsWithStdin(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, false, batchRequestInput{ctx: ctx, file: os.Stdin})
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	return options, err
}

func waitForBatch(ctx context.Context, cmd *cli.Command) (err error) {
	interval, timeout := cmd.Duration("poll-interval"), cmd.Duration("wait-timeout")
	if interval <= 0 || timeout < 0 {
		return batchError("--poll-interval must be positive, and --wait-timeout must be zero or positive.")
	}
	ctx, stop := batchSignalContext(ctx)
	defer stop()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	stopReset := context.AfterFunc(ctx, stop)
	defer stopReset()
	defer func() {
		if ctx.Err() != nil {
			err = batchContextError(errors.Join(ctx.Err(), context.Cause(ctx), err))
		}
	}()
	options, err := batchRequestOptions(ctx, cmd)
	if err != nil {
		return err
	}
	var destination *batchOutputDirectory
	if cmd.Name == "wait" && cmd.IsSet("output-dir") {
		destination, err = prepareBatchOutputDirectory(ctx, cmd.String("output-dir"))
		if err != nil {
			return err
		}
	}
	if batchHumanTerminal(ctx, cmd.Root()) {
		err = writeBatchStdout(ctx, os.Stderr, func(progress io.Writer) error {
			return pollBatch(ctx, cmd, options, interval, progress, destination)
		})
	} else {
		err = pollBatch(ctx, cmd, options, interval, nil, destination)
	}
	return err
}

func pollBatch(ctx context.Context, cmd *cli.Command, options []option.RequestOption, interval time.Duration, progress io.Writer, destination *batchOutputDirectory) error {
	client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
	var previous string
	for {
		var raw []byte
		requestOptions := append(options[:len(options):len(options)], option.WithResponseBodyInto(&raw))
		_, err := client.Batches.Get(ctx, cmd.String("batch-id"), requestOptions...)
		if err != nil {
			if ctx.Err() != nil {
				return batchContextError(ctx.Err())
			}
			return err
		}
		valid := gjson.ValidBytes(raw)
		if ctx.Err() != nil {
			return batchContextError(ctx.Err())
		}
		if !valid {
			return batchError("The API returned invalid batch JSON. Local waiting stopped; retrieve the batch again to check its status.")
		}
		batch := gjson.ParseBytes(raw)
		terminal, result := batchWaitResult(batch)
		if progress != nil {
			message := batchProgress(batch)
			if message != previous {
				if err := readable.WriteText(progress, message); err != nil {
					return errors.Join(err, result)
				}
				previous = message
			}
		}
		if terminal {
			var downloadErr error
			if destination != nil {
				downloadErr = collectBatchFiles(ctx, cmd, client, options, batch, destination)
			}
			outputErr := batchShowJSON(ctx, cmd, batch, "retrieve")
			if ctx.Err() != nil {
				return errors.Join(batchContextError(errors.Join(ctx.Err(), outputErr, downloadErr)), result)
			}
			return errors.Join(downloadErr, outputErr, result)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return batchContextError(ctx.Err())
		case <-timer.C:
		}
	}
}

func batchWaitResult(batch gjson.Result) (bool, error) {
	switch batch.Get("status").String() {
	case "validating", "in_progress", "finalizing", "cancelling":
		return false, nil
	case "completed":
		failed, ok := batchCount(batch.Get("request_counts.failed"))
		if ok && failed > 0 {
			return true, batchError("The batch completed with failed requests. Download its error file with batches download --file error.")
		}
		completed, completedOK := batchCount(batch.Get("request_counts.completed"))
		total, totalOK := batchCount(batch.Get("request_counts.total"))
		if !ok || !completedOK || !totalOK || completed != total {
			return true, batchError("The batch completed without consistent request counts. Inspect its response before treating requests as successful.")
		}
		return true, nil
	case "failed":
		return true, batchError("The batch failed. Inspect its errors before creating another batch.")
	case "expired":
		return true, batchError("The batch expired. Download any available output and error files before deciding what to retry.")
	case "cancelled":
		return true, batchError("The batch was canceled remotely. Download any available output and error files.")
	default:
		return true, batchError("The API returned an unknown batch status. Inspect the response; local waiting stopped.")
	}
}

func batchCount(value gjson.Result) (uint64, bool) {
	if value.Type != gjson.Number {
		return 0, false
	}
	count, err := strconv.ParseUint(value.Raw, 10, 64)
	return count, err == nil
}

func batchProgress(batch gjson.Result) string {
	status := batch.Get("status").String()
	completed, completedOK := batchCount(batch.Get("request_counts.completed"))
	failed, failedOK := batchCount(batch.Get("request_counts.failed"))
	total, totalOK := batchCount(batch.Get("request_counts.total"))
	validCounts := completedOK && failedOK && totalOK && completed <= total && failed <= total-completed
	if status == "completed" && validCounts && completed+failed == total {
		return fmt.Sprintf("Completed: %d succeeded, %d failed.", completed, failed)
	}
	if status == "in_progress" && validCounts {
		return fmt.Sprintf("Processing: %d of %d requests finished.", completed+failed, total)
	}
	return "Batch status: " + status
}

func batchShowJSON(ctx context.Context, cmd *cli.Command, batch gjson.Result, operation string) error {
	root := cmd.Root()
	diagnostics := batchTerminalWriter{Writer: batchDiagnosticWriter{ctx: ctx, file: os.Stderr}, file: os.Stderr}
	return writeBatchStdoutWithCleanup(ctx, root.Writer, batchOutputCleanupGrace, func(out io.Writer) error {
		return ShowJSON(batch, ShowJSONOpts{
			// Batch supplies signal cancellation and preserves its exit status.
			CallerHandlesSignals: true,
			Context:              ctx, Operation: "(resource) batches > (method) " + operation, OutputKind: OutputResponse,
			Format: root.String("format"), ExplicitFormat: root.IsSet("format"), RawOutput: root.Bool("raw-output"),
			Transform: root.String("transform"), Title: "batches " + operation, Stdout: out, Stderr: diagnostics,
		})
	})
}

func batchHumanTerminal(ctx context.Context, root *cli.Command) bool {
	return outputDiagnosticsAllowed(ctx) && isTerminal(root.Writer) && isTerminal(os.Stderr) && !root.Bool("debug") &&
		!root.Bool("raw-output") && root.String("transform") == "" &&
		root.String("transform-error") == "" && errorOutputFormat(root) == "text" &&
		resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text"
}

func showBatchNextCommand(batch gjson.Result, opts ShowJSONOpts) error {
	opts.setDefaults()
	if opts.Operation != "(resource) batches > (method) create" || opts.OutputKind != OutputResponse ||
		!isTerminal(opts.Stdout) || !isTerminal(opts.Stderr) || opts.Transform != "" || opts.RawOutput ||
		resolvedOutputFormat(opts) != "text" {
		return nil
	}
	cmd, ok := opts.Context.Value(batchCreateContextKey{}).(*cli.Command)
	if !ok || !batchHumanTerminal(opts.Context, cmd.Root()) {
		return nil
	}
	id := batch.Get("id").String()
	if id == "" {
		return nil
	}
	// A safe token works in all supported shells. Suppress unusual IDs rather
	// than interpolate untrusted syntax into a copyable command.
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return nil
		}
	}
	if batchHintNeedsRequestOptions(cmd.Root()) {
		return readable.WriteText(opts.Stderr, "To wait for this batch, reuse the same connection and authentication options with batches retrieve --wait.")
	}
	return readable.WriteText(opts.Stderr, fmt.Sprintf("Follow this batch:\n  %s batches retrieve --batch-id=%s --wait", clihelp.DisplayInvocation(cmd.Root()), id))
}

// A new process inherits environment configuration but loses command-line
// overrides. Compare effective values without ever printing private options.
func batchHintNeedsRequestOptions(root *cli.Command) bool {
	if root.IsSet("header") {
		return true
	}
	// Startup may temporarily normalize an invalid environment URL to this
	// explicit override. The next process would inherit the original value.
	if root.String("base-url") != "" {
		return true
	}
	for _, setting := range []struct{ flag, environment string }{
		{"api-key", "OPENAI_API_KEY"},
		{"admin-api-key", "OPENAI_ADMIN_KEY"},
		{"organization", "OPENAI_ORG_ID"},
		{"project", "OPENAI_PROJECT_ID"},
		{"webhook-secret", "OPENAI_WEBHOOK_SECRET"},
		{mtlsClientCertFileFlag, mtlsClientCertFileEnv},
		{mtlsClientKeyFileFlag, mtlsClientKeyFileEnv},
	} {
		if root.IsSet(setting.flag) && root.String(setting.flag) != os.Getenv(setting.environment) {
			return true
		}
	}
	return false
}
