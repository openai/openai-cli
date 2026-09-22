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
	"strings"

	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

const imageErrorContextKey = "image-error-context"

type imageErrorContext struct {
	command *cli.Command
	saving  bool
}

func beginImageErrorContext(command *cli.Command) *imageErrorContext {
	root := command.Root()
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}
	presentation := &imageErrorContext{command: command}
	root.Metadata[imageErrorContextKey] = presentation
	return presentation
}

// ShowFriendlyImageError only handles interactive image commands. API error
// formatting and redirected/script output retain their existing contract.
// This is presentation after failure, not a new credential check or retry policy.
func ShowFriendlyImageError(root *cli.Command, err error, stderr io.Writer) bool {
	presentation, _ := root.Metadata[imageErrorContextKey].(*imageErrorContext)
	if presentation == nil || !isTerminal(root.Writer) || !isTerminal(stderr) || terminalimage.InCI(os.Getenv) {
		return false
	}
	if !imageFriendlyErrorMode(root) {
		return false
	}
	message := imageErrorMessage(presentation, err)
	if message == "" {
		return false
	}
	fmt.Fprintln(stderr, message)
	return true
}

func imageFriendlyErrorMode(root *cli.Command) bool {
	return !root.IsSet("format-error") && root.String("transform-error") == "" &&
		!root.IsSet("format") && root.String("transform") == "" &&
		!root.Bool("raw-output") && !root.Bool("debug")
}

func imageErrorMessage(presentation *imageErrorContext, err error) string {
	command := presentation.command
	invocation, _ := command.Root().Metadata["help-invocation"].(string)
	if invocation == "" {
		invocation = "openai"
	}
	help := invocation + " help --all images " + command.Name
	setup := invocation + " help setup"
	var apierr *openai.Error
	isAPIError := errors.As(err, &apierr)
	// flagOptions owns required-field validation, including values from stdin.
	// Recognize its exact missing-prompt error without re-parsing user input.
	if !isAPIError && err.Error() == fmt.Sprintf("Required flag %q not set\nRun '%s --help' for usage information", "prompt", command.FullName()) {
		return "Describe the image you want with --prompt.\nTry: " + invocation + " images generate --prompt \"A tiny orange robot\""
	}
	if !presentation.saving {
		return ""
	}
	if isAPIError {
		message := imageAPIErrorMessage(apierr, command, help, setup)
		return message + "\nFor detailed API errors, add --format-error json to your command."
	}
	if errors.Is(err, context.Canceled) {
		return ""
	}
	var requestErr *url.Error
	if errors.As(err, &requestErr) {
		var timeout net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
			return "The image request timed out.\nThe API may have received it. Check your API usage before trying again."
		}
		return "Could not connect to the image API.\nCheck your connection, proxy, and any --base-url setting."
	}
	return ""
}

// Use status/code and known parameter names only. API messages can contain the
// user's prompt, rejected credential or control sequences; never echo them here.
// Error meanings: https://developers.openai.com/api/docs/guides/error-codes
func imageAPIErrorMessage(apierr *openai.Error, command *cli.Command, help, setup string) string {
	switch apierr.StatusCode {
	case http.StatusUnauthorized:
		if apierr.Request != nil && apierr.Request.URL != nil {
			if apierr.Request.URL.Hostname() != "api.openai.com" {
				return "The API endpoint rejected authentication.\nCheck the credentials required by your custom API endpoint."
			}
			auth := strings.TrimSpace(apierr.Request.Header.Get("Authorization"))
			if (auth == "" || strings.EqualFold(auth, "Bearer")) && apierr.Request.URL.User == nil {
				return "No API key was sent with this request.\nSet up your key: " + setup
			}
		}
		if apierr.Code == "invalid_api_key" {
			return "Your API key was not accepted.\nCheck or replace it using: " + setup
		}
		return "The API could not authenticate this request.\nCheck your key, organization, project and any IP restrictions.\nKey setup: " + setup
	case http.StatusForbidden:
		return "The API denied access to this request.\nCheck your project's image-model access, key permissions and supported region."
	case http.StatusRequestTimeout:
		return "The image request timed out.\nCheck your API usage before trying again."
	case http.StatusTooManyRequests:
		switch apierr.Code {
		case "credit_balance_exhausted":
			return "Your API credit balance is exhausted.\nCheck your organization's API billing before trying again."
		case "organization_spend_limit_exceeded":
			return "Your organization has reached its API spending limit.\nAsk an organization owner to review that limit before trying again."
		case "project_spend_limit_exceeded":
			return "Your project has reached its API spending limit.\nAsk a project owner to review that limit before trying again."
		case "organization_usage_limit_exceeded", "insufficient_quota", "billing_hard_limit_reached":
			return "Your API account has reached a usage or billing limit.\nCheck API billing and limits before trying again; waiting alone may not fix this."
		}
		if apierr.Type == "insufficient_quota" {
			return "Your API account has reached a usage or billing limit.\nCheck API billing and limits before trying again; waiting alone may not fix this."
		}
		return "The API is receiving requests too quickly.\nPause before trying again, and send fewer requests at once."
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound:
		if apierr.Code == "content_policy_violation" {
			return "The API declined this image request under its content policy.\nReview your description before trying again."
		}
		if apierr.Code == "model_not_found" {
			return "That image model is unavailable or your project cannot access it.\nCheck the model name and project access, or choose a model with --model."
		}
		if apierr.StatusCode == http.StatusBadRequest || apierr.StatusCode == http.StatusUnprocessableEntity {
			return readableAPIArgumentMessage(apierr, command)
		}
		return "The API could not accept this image request.\nCheck your model and image settings:\n  " + help
	default:
		if apierr.StatusCode >= 500 && apierr.StatusCode <= 599 {
			return fmt.Sprintf("The image service could not complete the request (HTTP %d).\nTry again later. If this continues, check the API service status.", apierr.StatusCode)
		}
		return fmt.Sprintf("The image request failed (HTTP %d).\nCheck your API configuration and image settings:\n  %s", apierr.StatusCode, help)
	}
}
