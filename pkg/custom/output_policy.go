package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

type outputPolicyKey struct{}
type outputInvocationKey struct{}

// One synchronous invocation owns this state. Nested actions retain its first identity.
type outputInvocation struct {
	command  *cli.Command
	label    string
	protocol bool
	context  context.Context
}

// RunWithOutputPolicy reports after the complete CLI run and its cleanup return.
// Direct Command.Run callers retain the existing action-scoped reporting boundary.
func RunWithOutputPolicy(ctx context.Context, root *cli.Command, run func(context.Context) error) error {
	invocation := &outputInvocation{}
	ctx = context.WithValue(ctx, outputInvocationKey{}, invocation)
	started := time.Now()
	err := run(ctx)
	elapsed := time.Since(started)
	err = exposeInvocationErrors(err)
	if invocation.command == nil {
		// Successful help, version, and completion need no action report.
		if err == nil || root.SkipFlagParsing {
			return err
		}
		invocation.command, invocation.label, invocation.protocol = parsedOutputCommand(root)
		if invocation.command == nil || root.Bool("help") || root.Bool("version") || invocation.command.Bool("help") {
			return err
		}
	} else {
		ctx = invocation.context
	}
	ctx = outputPolicyContext(ctx, invocation.command)
	return finishOutputPolicy(ctx, invocation.command, invocation.label, invocation.protocol, err, elapsed)
}

// The framework hides combined Action/After errors behind Errors(), not Unwrap().
// Adapt only those containers and the callback's joined cleanup errors.
func exposeInvocationErrors(err error) error {
	var causes []error
	switch combined := err.(type) {
	case cli.MultiError:
		causes = combined.Errors()
		for i, cause := range causes {
			causes[i] = exposeInvocationErrors(cause)
		}
		// The opaque original retains its identity without hiding ordered children.
		causes = append([]error{err}, causes...)
	case interface{ Unwrap() []error }:
		for _, cause := range combined.Unwrap() {
			causes = append(causes, exposeInvocationErrors(cause))
		}
		// Visit normalized operation errors before joined cleanup errors.
		causes = append(causes, err)
	default:
		return err
	}
	return &outputInvocationError{error: err, causes: causes}
}

type outputInvocationError struct {
	error
	causes []error
}

func (e *outputInvocationError) Unwrap() []error { return e.causes }

// Read parsed selectors, but emit only names from the declared command tree.
func parsedOutputCommand(root *cli.Command) (*cli.Command, string, bool) {
	command := root
	var path []string
	for command != nil {
		if command.Name == "help" || command.Name == "__complete" {
			return nil, "", false
		}
		if command.Args() == nil || !command.Args().Present() {
			break
		}
		child := command.Command(command.Args().First())
		if child == nil {
			break
		}
		path = append(path, child.Name)
		command = child
	}
	if command == nil {
		return nil, "", false
	}
	label := strings.Join(path, " ")
	if label == "" {
		label = root.Name
	}
	return command, label, command.Name == "@completion"
}

type outputPolicy struct {
	quiet       bool
	diagnostics bool
}

// Diagnostic failures belong to stderr, even inside stdout's stream lifecycle.
type diagnosticWriteError struct{ error }

func (e *diagnosticWriteError) Unwrap() error { return e.error }

func quietOutput(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	policy, _ := ctx.Value(outputPolicyKey{}).(outputPolicy)
	return policy.quiet
}

// Optional prose must never precede a selected machine-readable error payload.
func outputDiagnosticsAllowed(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	policy, ok := ctx.Value(outputPolicyKey{}).(outputPolicy)
	return !ok || policy.diagnostics
}

func outputPolicyContext(ctx context.Context, command *cli.Command) context.Context {
	root := command.Root()
	return context.WithValue(ctx, outputPolicyKey{}, outputPolicy{
		quiet: root.Bool("quiet"),
		diagnostics: !root.Bool("quiet") && errorOutputFormat(root) == "text" &&
			root.String("transform-error") == "",
	})
}

func writeOutputHint(opts ShowJSONOpts, message string) error {
	if !outputDiagnosticsAllowed(opts.Context) {
		return nil
	}
	if err := readable.WriteText(outputWriter{ctx: opts.Context, out: opts.Stderr}, message); err != nil {
		return &diagnosticWriteError{err}
	}
	return nil
}

func writeOutputReceipt(ctx context.Context, message string) error {
	if !outputDiagnosticsAllowed(ctx) {
		return nil
	}
	return readable.WriteText(outputWriter{ctx: ctx, out: os.Stderr}, message)
}

func configureOutputPolicy(root *cli.Command) {
	root.Flags = append(root.Flags,
		&cli.BoolFlag{Name: "quiet", Usage: "Suppress optional feedback. Keep data, help, errors and exit status. Overrides verbose.", HideDefault: true},
		&cli.BoolFlag{Name: "verbose", Usage: "Report command details on stderr. Omitted for machine errors; use --format-error text to enable.", HideDefault: true},
	)
	var visit func(*cli.Command, []string)
	visit = func(command *cli.Command, path []string) {
		if command.Name == "__complete" || command.Name == "help" {
			return
		}
		if command != root {
			path = append(slices.Clone(path), command.Name)
		}
		if command.Action != nil {
			next := command.Action
			// Capture declaration names, never the invoked executable or arguments.
			label := strings.Join(path, " ")
			protocol := command.Name == "@completion"
			command.Action = func(ctx context.Context, command *cli.Command) error {
				ctx = outputPolicyContext(ctx, command)
				if invocation, _ := ctx.Value(outputInvocationKey{}).(*outputInvocation); invocation != nil {
					if invocation.command == nil {
						invocation.command, invocation.label, invocation.protocol = command, label, protocol
						invocation.context = ctx
					}
					return next(ctx, command)
				}
				started := time.Now()
				err := next(ctx, command)
				elapsed := time.Since(started)
				return finishOutputPolicy(ctx, command, label, protocol, err, elapsed)
			}
		}
		for _, child := range command.Commands {
			visit(child, path)
		}
	}
	visit(root, nil)
}

func finishOutputPolicy(ctx context.Context, command *cli.Command, label string, protocol bool, err error, elapsed time.Duration) error {
	root := command.Root()
	if protocol && !command.Bool("install-picker") && !command.Bool("uninstall-picker") ||
		!root.Bool("verbose") || !outputDiagnosticsAllowed(ctx) {
		return err
	}
	// Optional feedback must not restart output after an interrupted action.
	var exit cli.ExitCoder
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &exit) && (exit.ExitCode() == 124 || exit.ExitCode() == 130 || exit.ExitCode() == 143) {
		return err
	}
	format := strings.ToLower(root.String("format"))
	if format == "" {
		format = "auto"
	}
	if !slices.Contains(OutputFormats, format) {
		format = "unknown"
	}
	diagnosticErr := writeVerboseResult(os.Stderr, label, format, err, elapsed)
	if diagnosticErr != nil {
		diagnosticErr = &diagnosticWriteError{diagnosticErr}
	}
	return errors.Join(err, diagnosticErr)
}

// Diagnostics use declared command names, validated formats, local timing, and a fixed outcome.
// An action returning nil confirms command completion, not asynchronous API work.
func writeVerboseResult(out io.Writer, command, format string, failure error, elapsed time.Duration) error {
	result := "completed"
	if failure != nil {
		result = "failed"
	}
	_, err := fmt.Fprintf(outputWriter{ctx: context.Background(), out: out},
		"Command: %s\nFormat option: %s\nElapsed: %s\nCommand result: %s\n",
		command, format, elapsed.Round(time.Millisecond).String(), result)
	return err
}
