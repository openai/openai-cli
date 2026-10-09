package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type webhookCreateSettings struct {
	name, url string
	events    []string
}

// Messages contain only local guidance. Causes retain status and identity;
// their possibly sensitive bodies or URLs never enter this message.
type webhookWorkflowError struct {
	message string
	cause   error
}

func (e *webhookWorkflowError) Error() string { return e.message }
func (e *webhookWorkflowError) Unwrap() error { return e.cause }

const webhookCreateCatalogRetry = "No endpoint was created. " +
	"Use webhooks event-types list --format json with your original authentication, project, organization, and API settings. " +
	"Check your project and access, then retry webhooks create with those settings. " +
	"Use openai webhooks create --help for explicit flags."

// Explicit request and output choices retain the generated command's behavior.
func webhookCreateWorkflow(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		out, ok := command.Root().Writer.(*os.File)
		if !ok || out != os.Stdout || !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(out.Fd()) ||
			!webhookCreateRequested(command, os.Getenv) {
			return next(ctx, command)
		}
		return withWebhookCreateInterrupts(ctx, func(ctx context.Context) error {
			if _, err := io.WriteString(outputWriter{ctx: ctx, out: out}, "Loading webhook event types for your project...\n"); err != nil {
				return &webhookWorkflowError{"Could not display webhook setup. No endpoint was created. " +
					"Check terminal output, then retry webhooks create with your original authentication, project, organization, and API settings. " +
					"Use openai webhooks create --help for explicit flags.", err}
			}
			return runWebhookCreateFlow(ctx, command, next, discoverWebhookCreateEvents,
				func(ctx context.Context, events []string) (webhookCreateSettings, bool, error) {
					return runWebhookCreatePicker(ctx, os.Stdin, out, events)
				})
		})
	}
}

func webhookCreateRequested(command *cli.Command, getenv func(string) string) bool {
	if command.Args().Len() != 0 || getenv("CI") != "" || strings.EqualFold(getenv("TERM"), "dumb") {
		return false
	}
	for _, flag := range command.Flags {
		if flag.IsSet() {
			return false
		}
	}
	root := command.Root()
	for _, name := range []string{"format", "format-error", "transform", "transform-error", "raw-output", "quiet", "debug"} {
		if root.IsSet(name) {
			return false
		}
	}
	return resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text"
}

func discoverWebhookCreateEvents(ctx context.Context, command *cli.Command) ([]string, error) {
	// The root has no endpoint fields. Discovery must not consume stdin or
	// validate create's still-empty required fields. Shared options retain
	// explicit headers, debug policy, authentication, project and mTLS handling.
	options, err := FlagOptions(command.Root(), apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
	if err != nil {
		return nil, err
	}
	client := openai.NewClient(GetDefaultRequestOptions(command)...)
	var raw []byte
	options = append(options, option.WithResponseBodyInto(&raw))
	if _, err := client.Webhooks.EventTypes.List(ctx, options...); err != nil {
		var apiError *openai.Error
		if errors.As(err, &apiError) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, &webhookWorkflowError{"Could not load webhook event types. Check your connection and API settings. " + webhookCreateCatalogRetry, err}
	}
	return parseWebhookCreateEvents(raw)
}

func parseWebhookCreateEvents(raw []byte) ([]string, error) {
	value := gjson.ParseBytes(raw)
	if !gjson.ValidBytes(raw) || !value.IsObject() {
		return nil, &webhookWorkflowError{"Could not read available webhook events. " + webhookCreateCatalogRetry, nil}
	}
	counts := map[string]int{}
	value.ForEach(func(key, _ gjson.Result) bool {
		if key.Str == "object" || key.Str == "data" {
			counts[key.Str]++
		}
		return true
	})
	if counts["object"] != 1 || counts["data"] != 1 || value.Get("object").Type != gjson.String ||
		value.Get("object").String() != "list" || !value.Get("data").IsArray() {
		return nil, &webhookWorkflowError{"The webhook event catalog has missing, repeated, or invalid fields. " + webhookCreateCatalogRetry, nil}
	}
	var events []string
	seen := map[string]bool{}
	for _, entry := range value.Get("data").Array() {
		if entry.Type != gjson.String || strings.TrimSpace(entry.String()) == "" {
			return nil, &webhookWorkflowError{"The webhook event list contains an invalid entry. " + webhookCreateCatalogRetry, nil}
		}
		event := entry.String()
		if !seen[event] {
			seen[event] = true
			events = append(events, event)
		}
	}
	if len(events) == 0 {
		return nil, &webhookWorkflowError{"No webhook event types are available for this project. " + webhookCreateCatalogRetry, nil}
	}
	return events, nil
}

func runWebhookCreateFlow(ctx context.Context, command *cli.Command, next cli.ActionFunc,
	discover func(context.Context, *cli.Command) ([]string, error),
	pick func(context.Context, []string) (webhookCreateSettings, bool, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	events, err := discover(ctx, command)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	settings, confirmed, err := pick(ctx, events)
	if err != nil {
		return err
	}
	if !confirmed {
		return cli.Exit("", 130)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWebhookCreateSettings(settings); err != nil {
		return err
	}
	for _, selected := range settings.events {
		if !slices.Contains(events, selected) {
			return &webhookWorkflowError{"A selected event is no longer available. " + webhookCreateCatalogRetry, nil}
		}
	}
	return runWebhookCreateAction(ctx, command, settings, next)
}

// Keep the original flag objects, metadata and parsed state untouched. The
// generated create handler reads these values through ExtractRequestContents.
// The existing literal marker prevents both @file expansion and \@ removal.
type webhookCreateStringFlag struct {
	*requestflag.Flag[string]
	literal any
}

func (f *webhookCreateStringFlag) Get() any  { return f.literal }
func (*webhookCreateStringFlag) IsSet() bool { return true }

type webhookCreateEventsFlag struct {
	*requestflag.Flag[[]string]
	literal any
}

func (f *webhookCreateEventsFlag) Get() any  { return f.literal }
func (*webhookCreateEventsFlag) IsSet() bool { return true }

func runWebhookCreateAction(ctx context.Context, command *cli.Command, settings webhookCreateSettings, next cli.ActionFunc) error {
	original := command.Flags
	prepared := slices.Clone(original)
	replaced := 0
	for i, candidate := range prepared {
		switch flag := candidate.(type) {
		case *requestflag.Flag[string]:
			value := ""
			switch {
			case flag.Name == "name" && flag.BodyPath == "name":
				value = settings.name
			case flag.Name == "url" && flag.BodyPath == "url":
				value = settings.url
			default:
				continue
			}
			if flag.Validator != nil {
				if err := flag.Validator(value); err != nil {
					return err
				}
			}
			prepared[i] = &webhookCreateStringFlag{flag, protectStdinValue(value)}
			replaced++
		case *requestflag.Flag[[]string]:
			if flag.Name == "event-type" && flag.BodyPath == "event_types" {
				if flag.Validator != nil {
					if err := flag.Validator(slices.Clone(settings.events)); err != nil {
						return err
					}
				}
				prepared[i] = &webhookCreateEventsFlag{flag, protectStdinValue(settings.events)}
				replaced++
			}
		}
	}
	if replaced != 3 {
		return &webhookWorkflowError{"The webhook creation form does not match this command. No endpoint was created. " +
			"Use openai webhooks create --help for explicit flags.", nil}
	}
	command.Flags = prepared
	defer func() { command.Flags = original }()
	err := next(ctx, command)
	if errors.Is(err, context.Canceled) {
		return &webhookCreateCanceledError{cause: err}
	}
	return webhookCreateOutcomeError(err)
}

// Only a confirmed dispatch can produce this marker. The ordinary error
// presenter owns recovery output, so cancellation adds no synchronous writes.
type webhookCreateCanceledError struct{ cause error }

func (*webhookCreateCanceledError) Error() string {
	return "Request canceled. The create request may have reached the API. " +
		"Use webhooks list with your original authentication, project, organization, and API settings before repeating create."
}

func (e *webhookCreateCanceledError) Unwrap() error { return e.cause }

// Reuse after a create dispatch or create-result write. Preserve existing API
// errors, cancellation, and already-scoped guidance without duplicate wrapping.
func webhookCreateOutcomeError(err error) error {
	var apiError *openai.Error
	var workflow *webhookWorkflowError
	if err == nil || errors.As(err, &apiError) || errors.Is(err, context.Canceled) || errors.As(err, &workflow) {
		return err
	}
	// Never reopen or resubmit after dispatch: a transport or output failure
	// can occur after the API creates the endpoint and returns its only secret.
	return &webhookWorkflowError{"Webhook creation did not finish cleanly. The endpoint may already exist. " +
		"Use webhooks list with your original authentication, project, organization, and API settings before repeating create. " +
		"Use openai webhooks retrieve --help to inspect an existing endpoint.", err}
}

type webhookCreateInterrupt struct {
	error
	code int
}

func (e *webhookCreateInterrupt) Unwrap() error { return e.error }
func (e *webhookCreateInterrupt) ExitCode() int { return e.code }

// One signal context covers discovery, the form and the generated request.
// The renderer restores terminal modes before cancellation reaches main.
func withWebhookCreateInterrupts(parent context.Context, run func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	defer func() {
		signal.Stop(signals)
		cancel(nil)
		<-done
	}()
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			} else if received == syscall.SIGHUP {
				code = 129
			}
			cancel(&webhookCreateInterrupt{context.Canceled, code})
		case <-ctx.Done():
		}
	}()
	err := run(ctx)
	var interrupt *webhookCreateInterrupt
	if errors.As(context.Cause(ctx), &interrupt) && (err == nil || errors.Is(err, context.Canceled)) {
		return &webhookCreateInterrupt{errors.Join(err, context.Canceled), interrupt.code}
	}
	return err
}
