package webhooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/request"
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
type WorkflowError struct {
	Message string
	Cause   error
}

func (e *WorkflowError) Error() string { return e.Message }
func (e *WorkflowError) Unwrap() error { return e.Cause }

// HTTP failures after creation can leave an endpoint behind. Discovery errors
// already carry their known outcome; local validation errors never imply dispatch.
func webhookCreateRequestError(ctx context.Context, command *cli.Command, err error) error {
	var apiError *openai.Error
	var workflow *WorkflowError
	if errors.Is(err, context.Canceled) || errors.As(err, &workflow) {
		return err
	}
	message := ""
	if errors.As(err, &apiError) {
		if apiError.StatusCode != http.StatusRequestTimeout && apiError.StatusCode < 500 {
			return err
		}
	} else {
		// Assembly vets the prefix and readable error policy before guidance.
		var transport *url.Error
		if !errors.As(err, &transport) || transport.Op != "Post" {
			return err
		}
		state, _ := ctx.Value(webhookCommandKey{}).(webhookCommandContext)
		if state.options.ReadableTransportFailure == nil {
			return err
		}
		prefix, ok := state.options.ReadableTransportFailure(command, err)
		if !ok {
			return err
		}
		message = prefix + "\n"
	}
	message += "Webhook creation is unconfirmed. The endpoint may already exist.\n" +
		"Before repeating create, check the endpoint list for the requested name and URL."
	if list := webhookFollowupCommand(ctx, "webhooks", "list"); list != "" {
		message += "\nList endpoints: " + list
	} else {
		message += "\nUse webhooks list with your original authentication, project, organization, and API settings."
	}
	return &WorkflowError{message, err}
}

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
			if _, err := io.WriteString(output.Writer{Context: ctx, Sink: out}, "Loading webhook event types for your project...\n"); err != nil {
				return &WorkflowError{"Could not display webhook setup. No endpoint was created. " +
					"Check terminal output, then retry webhooks create with your original authentication, project, organization, and API settings. " +
					"Use openai webhooks create --help for explicit flags.", err}
			}
			return runWebhookCreateFlow(ctx, command, next, discoverWebhookCreateEvents,
				func(ctx context.Context, events []string) (webhookCreateSettings, bool, error) {
					return runWebhookCreatePicker(ctx, os.Stdin, out, events, func(ctx context.Context) ([]webhookEndpoint, error) {
						return discoverWebhookEndpoints(ctx, command)
					})
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
	return output.ResolveFormat(output.FormatOptions{Format: root.String("format")}) == "text"
}

func discoverWebhookCreateEvents(ctx context.Context, command *cli.Command) ([]string, error) {
	// The root has no endpoint fields. Discovery must not consume stdin or
	// validate create's still-empty required fields. Shared options retain
	// explicit headers, debug policy, authentication, project and mTLS handling.
	state, _ := ctx.Value(webhookCommandKey{}).(webhookCommandContext)
	options, err := state.options.PrepareDiscovery(command.Root())
	if err != nil {
		return nil, err
	}
	client := openai.NewClient(state.options.DefaultRequestOptions(command)...)
	var raw []byte
	options = append(options, option.WithResponseBodyInto(&raw))
	if _, err := client.Webhooks.EventTypes.List(ctx, options...); err != nil {
		var apiError *openai.Error
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		if errors.As(err, &apiError) {
			return nil, &WorkflowError{"Could not load webhook event types.\nNo endpoint was created.", err}
		}
		return nil, &WorkflowError{"Could not load webhook event types. Check your connection and API settings. " + webhookCreateCatalogRetry, err}
	}
	return parseWebhookCreateEvents(raw)
}

func parseWebhookCreateEvents(raw []byte) ([]string, error) {
	value := gjson.ParseBytes(raw)
	if !gjson.ValidBytes(raw) || !value.IsObject() {
		return nil, &WorkflowError{"Could not read available webhook events. " + webhookCreateCatalogRetry, nil}
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
		return nil, &WorkflowError{"The webhook event catalog has missing, repeated, or invalid fields. " + webhookCreateCatalogRetry, nil}
	}
	var events []string
	seen := map[string]bool{}
	for _, entry := range value.Get("data").Array() {
		if entry.Type != gjson.String || strings.TrimSpace(entry.String()) == "" {
			return nil, &WorkflowError{"The webhook event list contains an invalid entry. " + webhookCreateCatalogRetry, nil}
		}
		event := entry.String()
		if !unavailableWebhookEvent(event) && !seen[event] {
			seen[event] = true
			events = append(events, event)
		}
	}
	if len(events) == 0 {
		if value.Get("data.#").Int() > 0 {
			return nil, &WorkflowError{"The available webhook events are not supported by the CLI. " +
				"No endpoint was created. Use the Platform API directly for these events.", nil}
		}
		return nil, &WorkflowError{"No webhook event types are available for this project. " + webhookCreateCatalogRetry, nil}
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
			return &WorkflowError{"A selected event is no longer available. " + webhookCreateCatalogRetry, nil}
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
			prepared[i] = &webhookCreateStringFlag{flag, request.ProtectStdinValue(value)}
			replaced++
		case *requestflag.Flag[[]string]:
			if flag.Name == "event-type" && flag.BodyPath == "event_types" {
				if flag.Validator != nil {
					if err := flag.Validator(slices.Clone(settings.events)); err != nil {
						return err
					}
				}
				prepared[i] = &webhookCreateEventsFlag{flag, request.ProtectStdinValue(settings.events)}
				replaced++
			}
		}
	}
	if replaced != 3 {
		return &WorkflowError{"The webhook creation form does not match this command. No endpoint was created. " +
			"Use openai webhooks create --help for explicit flags.", nil}
	}
	command.Flags = prepared
	defer func() { command.Flags = original }()
	err := next(ctx, command)
	if errors.Is(err, context.Canceled) {
		return &CreateCanceledError{Cause: err}
	}
	return webhookCreateOutcomeError(err)
}

// Only a confirmed dispatch can produce this marker. The ordinary error
// presenter owns recovery output, so cancellation adds no synchronous writes.
type CreateCanceledError struct{ Cause error }

func (*CreateCanceledError) Error() string {
	return "Request canceled. The create request may have reached the API. " +
		"Use webhooks list with your original authentication, project, organization, and API settings before repeating create."
}

func (e *CreateCanceledError) Unwrap() error { return e.Cause }

// Reuse after a create dispatch or create-result write. Preserve existing API
// errors, cancellation, and already-scoped guidance without duplicate wrapping.
func webhookCreateOutcomeError(err error) error {
	var apiError *openai.Error
	var workflow *WorkflowError
	if err == nil || errors.As(err, &apiError) || errors.Is(err, context.Canceled) || errors.As(err, &workflow) {
		return err
	}
	// Never reopen or resubmit after dispatch: a transport or output failure
	// can occur after the API creates the endpoint and returns its only secret.
	return &WorkflowError{"Webhook creation did not finish cleanly. The endpoint may already exist. " +
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
