package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

// Only the prompt already displayed by the picker may enter loading feedback.
type imagePickerLoadingPromptKey struct{}

// Only a bare interactive command enters the picker. Explicit flags and piped
// bodies retain their existing validation, defaults and output behavior.
func imagePickerWorkflow(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) (resultErr error) {
		out, ok := command.Root().Writer.(*os.File)
		if !ok || out != os.Stdout || !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(out.Fd()) ||
			strings.EqualFold(os.Getenv("TERM"), "dumb") || !imagePickerRequested(command) {
			return next(ctx, command)
		}
		// Keep the executable binding through initial input and every request.
		// Each native writer still finishes before the picker reads input again.
		ctx, closeOutput := prepareNativeImageOutput(ctx, command)
		defer func() {
			resultErr = errors.Join(resultErr, closeOutput())
		}()
		return runImagePickerDrafts(ctx, command, next, os.Stderr, func(options imagePickerOptions) (imagePickerResult, error) {
			return runImagePickerSession(ctx, os.Stdin, out, options)
		})
	}
}

// Sessions can restore a local draft. Each request still requires an explicit
// selection from the picker; restoring text never starts generation.
func runImagePickerDrafts(ctx context.Context, command *cli.Command, next cli.ActionFunc, diagnostics io.Writer, pick func(imagePickerOptions) (imagePickerResult, error)) error {
	options := imagePickerOptions{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := pick(options)
		if err != nil {
			return err
		}
		if result.Canceled {
			code := result.ExitCode
			if code == 0 {
				code = 130
			}
			return cli.Exit("", code)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if result.PrintOnly {
			return nil
		}
		options = imagePickerOptions{Prompt: result.settings.prompt, initial: &result.settings, resuming: true}
		requestCtx := context.WithValue(ctx, imagePickerLoadingPromptKey{}, result.settings.prompt)
		if err := runImagePickerAction(requestCtx, command, result.Args, next); err != nil {
			if ctx.Err() != nil || !imagePickerRecoverableRequest(err) {
				return err
			}
			// ErrWriter can be an unflushed usage buffer. Show the failure on
			// real stderr before returning input ownership to the picker.
			if displayErr := writeImagePickerRequestMessage(ctx, diagnostics, command.Root(), err, result.draftSaved); displayErr != nil {
				return errors.Join(err, displayErr)
			}
			options.initialNote = "Your prompt and settings are still here. Press Enter to try again."
			if imagePickerCredentialFailure(err) {
				options.initialNote = "Draft not saved. Copy before Ctrl+C."
				if result.draftSaved {
					options.initialNote = "Draft saved."
				}
			}
		} else {
			options.initialNote = "All images saved. Edit your prompt or settings to create more."
		}
	}
}

func imagePickerCredentialFailure(err error) bool {
	var apiError *openai.Error
	return errors.As(err, &apiError) && (apiError.StatusCode == http.StatusUnauthorized || apiError.StatusCode == http.StatusForbidden)
}

// Escape all text before adding local heading emphasis. Redirected diagnostics
// and terminals that opt out of styling retain the same readable plain layout.
func writeImagePickerRequestMessage(ctx context.Context, out io.Writer, root *cli.Command, failure error, draftSaved bool) error {
	message := readable.Text(imagePickerRequestMessage(root, failure, draftSaved))
	if imagePickerCredentialFailure(failure) && isTerminal(out) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		heading, body, _ := strings.Cut(message, "\n")
		message = lipgloss.NewStyle().Bold(true).Render(heading) + "\n" + body
	}
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	_, err := io.WriteString(outputWriter{ctx: ctx, out: out}, message)
	return err
}

// The picker needs a next step, not API troubleshooting output. These local
// messages never include server prose, credentials, request values, or server URLs.
// Flag-based and structured commands keep the shared error presenter.
func imagePickerRequestMessage(root *cli.Command, err error, draftSaved bool) string {
	var apiError *openai.Error
	if errors.As(err, &apiError) {
		if imagePickerCredentialFailure(err) {
			heading, cause := "Authentication failed", "Your API key was not accepted."
			if apiError.StatusCode == http.StatusForbidden {
				heading, cause = "Access denied", "Your key or project lacks permission."
			}
			draft := "Copy your prompt. Ctrl+C exits."
			if draftSaved {
				draft = "Your draft is saved. Ctrl+C exits."
			}
			return fmt.Sprintf("  %s\n  %s\n\n"+
				"  %s\n"+
				"  Setup guide: %s help setup\n"+
				"  Reopen the picker in this shell.", heading, cause, draft, errorHelpInvocation(root))
		}
		switch apiError.StatusCode {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			switch apiError.Code {
			case "content_policy_violation":
				return "This prompt wasn't accepted by the image safety checks. Try a different description."
			case "model_not_found":
				return "This model isn't available for your project. Try another model."
			}
			return "Couldn't create your image. Try changing the prompt or settings."
		case http.StatusNotFound:
			return "The model or image service wasn't found. Try another model or check your API address."
		case http.StatusTooManyRequests:
			switch apiError.Code {
			case "insufficient_quota", "billing_hard_limit_reached", "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "organization_usage_limit_exceeded":
				return "Your project has reached its API usage or billing limit. Check your billing and limits before trying again."
			}
			return "Too many requests right now. Wait a moment before trying again."
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return "The image request timed out. Check API usage before trying again."
		}
		return "The image service couldn't finish this request. Check API usage before trying again."
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "The image request timed out. Check API usage before trying again."
	}
	return "The connection to the image service failed. Check your connection and API usage before trying again."
}

// Only request failures can reopen a draft. Saving/preview failures may follow
// paid work or partial output; terminal failures and cancellation must exit.
// Every member of a joined failure must be recoverable.
func imagePickerRecoverableRequest(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	switch failure := err.(type) {
	case cli.ExitCoder, *imageSavingError, *outputWriteError:
		return false
	case interface{ Unwrap() []error }:
		causes := failure.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !imagePickerRecoverableRequest(cause) {
				return false
			}
		}
		return true
	case *openai.Error, *url.Error, net.Error:
		return true
	case interface{ Unwrap() error }:
		return imagePickerRecoverableRequest(failure.Unwrap())
	default:
		return false
	}
}

func runImagePickerAction(ctx context.Context, command *cli.Command, args []string, next cli.ActionFunc) error {
	for i := 2; i < len(args); i += 2 {
		restore, err := setImagePickerFlag(command, strings.TrimPrefix(args[i], "--"), args[i+1])
		if err != nil {
			return err
		}
		defer restore()
	}
	// Each request uses the existing saving workflow and generated SDK action.
	// Restore flags before the next draft, including after failed requests.
	return next(ctx, command)
}

// Isolate selections from the shared command tree, including after a failed
// action. A struct copy alone would still share the flag's parsed value.
func setImagePickerFlag(command *cli.Command, name, value string) (func(), error) {
	for _, candidate := range command.Flags {
		if !slices.Contains(candidate.Names(), name) {
			continue
		}
		switch flag := candidate.(type) {
		case *requestflag.Flag[string]:
			return setTemporaryImagePickerFlag(flag, name, value)
		case *requestflag.Flag[*string]:
			return setTemporaryImagePickerFlag(flag, name, value)
		case *requestflag.Flag[*int64]:
			return setTemporaryImagePickerFlag(flag, name, value)
		case *cli.StringFlag:
			original, replacement := *flag, *flag
			// Do not mutate an existing parsed value or external destination.
			replacement.Destination = nil
			if err := replacement.PreParse(); err != nil {
				return nil, err
			}
			if err := replacement.Set(name, value); err != nil {
				return nil, err
			}
			*flag = replacement
			return func() { *flag = original }, nil
		}
		break
	}
	return nil, fmt.Errorf("unsupported image picker setting: %s", name)
}

func setTemporaryImagePickerFlag[T string | *string | *int64](flag *requestflag.Flag[T], name, value string) (func(), error) {
	original, replacement := *flag, *flag
	if err := replacement.PreParse(); err != nil {
		return nil, err
	}
	if err := replacement.Set(name, value); err != nil {
		return nil, err
	}
	*flag = replacement
	return func() { *flag = original }, nil
}

func imagePickerRequested(command *cli.Command) bool {
	if command.Args().Len() != 0 {
		return false
	}
	for _, flag := range command.Flags {
		if flag.IsSet() {
			return false
		}
	}
	for _, name := range []string{"format", "format-error", "transform", "transform-error", "raw-output"} {
		if command.Root().IsSet(name) {
			return false
		}
	}
	return true
}
