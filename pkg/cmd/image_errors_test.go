package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

// Each fixture owns its command tree. No global generated commands, credentials,
// network requests, or native terminal settings are used by these tests.
func imageErrorTestContext(t *testing.T, args []string, invocation string) (*cli.Command, *imageErrorContext) {
	t.Helper()
	var presentation *imageErrorContext
	generate := &cli.Command{
		Name: "generate", HideHelpCommand: true,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "prompt", BodyPath: "prompt"},
			&requestflag.Flag[*int64]{Name: "n", BodyPath: "n"},
			&requestflag.Flag[*string]{Name: "output-format", BodyPath: "output_format"},
			&requestflag.Flag[*string]{Name: "model", BodyPath: "model"},
		},
		Action: func(_ context.Context, command *cli.Command) error {
			presentation = beginImageErrorContext(command)
			presentation.saving = true
			return nil
		},
	}
	root := &cli.Command{
		Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, HideHelpCommand: true,
		Metadata: map[string]any{"help-invocation": invocation},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "auto"},
			&cli.StringFlag{Name: "transform"},
			&cli.StringFlag{Name: "transform-error"},
			&cli.BoolFlag{Name: "raw-output"},
			&cli.BoolFlag{Name: "debug"},
		},
		Commands: []*cli.Command{
			{Name: "images", Commands: []*cli.Command{generate}},
			{Name: "models", Commands: []*cli.Command{{Name: "list", Action: func(context.Context, *cli.Command) error { return nil }}}},
		},
	}
	if args == nil {
		args = []string{"images", "generate"}
	}
	if err := root.Run(t.Context(), append([]string{"openai"}, args...)); err != nil {
		t.Fatal(err)
	}
	return root, presentation
}

func imageErrorTestAPI(t *testing.T, status int, endpoint, auth, code, parameter string) *openai.Error {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		request.Header.Set("Authorization", auth)
	}
	raw, err := json.Marshal(map[string]string{
		"code": code, "param": parameter,
		"message": "synthetic-private-prompt synthetic-rejected-key \x1b]0;untrusted-title\a",
		"extra":   "synthetic-raw-detail",
	})
	if err != nil {
		t.Fatal(err)
	}
	apierr := &openai.Error{}
	if err := json.Unmarshal(raw, apierr); err != nil {
		t.Fatal(err)
	}
	apierr.StatusCode, apierr.Request, apierr.Response = status, request, &http.Response{StatusCode: status}
	return apierr
}

func TestImageErrorMessageAuthentication(t *testing.T) {
	_, presentation := imageErrorTestContext(t, nil, "./openai")
	for _, test := range []struct {
		name, endpoint, auth, code, want string
	}{
		{"missing", "https://api.openai.com/v1/images/generations", "", "", "No API key was sent"},
		{"empty bearer", "https://api.openai.com/v1/images/generations", "Bearer ", "", "No API key was sent"},
		{"invalid key", "https://api.openai.com/v1/images/generations", "Bearer synthetic-rejected-key", "invalid_api_key", "Your API key was not accepted"},
		{"generic auth", "https://api.openai.com/v1/images/generations", "Bearer synthetic-rejected-key", "", "could not authenticate"},
		{"custom header auth", "https://api.openai.com/v1/images/generations", "Custom synthetic-rejected-key", "", "could not authenticate"},
		{"URL basic auth", "https://synthetic-user:synthetic-password@api.openai.com/v1/images/generations", "", "", "could not authenticate"},
		{"custom endpoint", "https://images.example.test/v1/images/generations", "", "invalid_api_key", "credentials required by your custom API endpoint"},
		{"custom endpoint with auth", "https://images.example.test/v1/images/generations", "Bearer synthetic-rejected-key", "", "credentials required by your custom API endpoint"},
		{"hostname suffix", "https://api.openai.com.example.test/v1/images/generations", "", "", "credentials required by your custom API endpoint"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := imageErrorMessage(presentation, imageErrorTestAPI(t, http.StatusUnauthorized, test.endpoint, test.auth, test.code, ""))
			if !strings.Contains(got, test.want) {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
			if strings.Contains(got, "No API key was sent") && test.want != "No API key was sent" {
				t.Fatalf("authenticated or custom endpoint request reported a missing key: %q", got)
			}
			assertImageErrorSafe(t, got)
		})
	}
}

func TestImageErrorMessageAPIStatus(t *testing.T) {
	_, presentation := imageErrorTestContext(t, nil, "./openai")
	for _, test := range []struct {
		name, code, kind, parameter, want string
		status                            int
	}{
		{name: "permission", status: 403, want: "key permissions"},
		{name: "request timeout", status: 408, want: "Check your API usage before trying again"},
		{name: "rate", status: 429, code: "rate_limit_exceeded", want: "Pause before trying again"},
		{name: "credits", status: 429, code: "credit_balance_exhausted", want: "credit balance is exhausted"},
		{name: "organization spend", status: 429, code: "organization_spend_limit_exceeded", want: "organization has reached its API spending limit"},
		{name: "project spend", status: 429, code: "project_spend_limit_exceeded", want: "project has reached its API spending limit"},
		{name: "organization usage", status: 429, code: "organization_usage_limit_exceeded", want: "waiting alone may not fix this"},
		{name: "legacy quota", status: 429, code: "insufficient_quota", want: "waiting alone may not fix this"},
		{name: "legacy billing", status: 429, code: "billing_hard_limit_reached", want: "waiting alone may not fix this"},
		{name: "quota type", status: 429, kind: "insufficient_quota", want: "waiting alone may not fix this"},
		{name: "content policy", status: 400, code: "content_policy_violation", want: "content policy"},
		{name: "model missing", status: 404, code: "model_not_found", want: "choose a model with --model"},
		{name: "body parameter", status: 400, parameter: "output_format", want: "The API rejected --output-format."},
		{name: "short parameter", status: 422, parameter: "n", want: "The API rejected -n."},
		{name: "unknown parameter", status: 400, parameter: "synthetic-private-prompt\x1b[2J", want: "could not accept this image request"},
		{name: "service error", status: 503, want: "HTTP 503"},
		{name: "unknown status", status: 418, want: "HTTP 418"},
	} {
		t.Run(test.name, func(t *testing.T) {
			apierr := imageErrorTestAPI(t, test.status, "https://api.openai.com/v1/images/generations?token=synthetic-secret-query", "Bearer synthetic-rejected-key", test.code, test.parameter)
			apierr.Type = test.kind
			got := imageErrorMessage(presentation, fmt.Errorf("wrapped sensitive context: %w", apierr))
			if !strings.Contains(got, test.want) || !strings.Contains(got, "--format-error json") {
				t.Fatalf("message = %q, want %q and detailed-error escape hatch", got, test.want)
			}
			if test.status == 429 && test.code != "rate_limit_exceeded" && strings.Contains(got, "Pause before trying again") {
				t.Fatalf("quota failure was represented as temporary rate limiting: %q", got)
			}
			assertImageErrorSafe(t, got)
		})
	}
}

func assertImageErrorSafe(t *testing.T, message string) {
	t.Helper()
	for _, forbidden := range []string{"synthetic-private-prompt", "synthetic-rejected-key", "synthetic-secret-query", "synthetic-password", "synthetic-raw-detail", "untrusted-title", "wrapped sensitive context", "\x1b", "\a", "https://"} {
		if strings.Contains(message, forbidden) {
			t.Errorf("friendly error exposed %q: %q", forbidden, message)
		}
	}
}

func TestImageErrorMessageNetworkAndFallback(t *testing.T) {
	_, presentation := imageErrorTestContext(t, nil, "./openai")
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"timeout", &url.Error{Op: "Post", URL: "https://images.example.test/?token=synthetic-secret-query", Err: context.DeadlineExceeded}, "API may have received it"},
		{"network timeout", &url.Error{Op: "Post", URL: "https://images.example.test/?token=synthetic-secret-query", Err: &net.DNSError{Name: "synthetic-private-prompt", IsTimeout: true}}, "API may have received it"},
		{"connection", &url.Error{Op: "Post", URL: "https://images.example.test/?token=synthetic-secret-query", Err: errors.New("synthetic-private-prompt")}, "Check your connection, proxy"},
		{"local error", errors.New("synthetic-private-prompt"), ""},
		{"cancellation", context.Canceled, ""},
		{"request cancellation", &url.Error{Op: "Post", URL: "https://images.example.test/", Err: context.Canceled}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := imageErrorMessage(presentation, test.err)
			if test.want == "" && got != "" || test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
			assertImageErrorSafe(t, got)
		})
	}
	presentation.saving = false
	apierr := imageErrorTestAPI(t, 401, "https://api.openai.com/v1/images/generations", "", "", "")
	if got := imageErrorMessage(presentation, apierr); got != "" {
		t.Fatalf("API-output mode received friendly replacement: %q", got)
	}
}

func TestImageErrorMessageToleratesMissingRequestMetadata(t *testing.T) {
	_, presentation := imageErrorTestContext(t, nil, "./openai")
	for _, test := range []struct {
		name string
		err  *openai.Error
		want string
	}{
		{"missing request and response", &openai.Error{StatusCode: 401}, "could not authenticate"},
		{"missing request URL", &openai.Error{StatusCode: 401, Request: &http.Request{}}, "could not authenticate"},
		{"invalid key without response", &openai.Error{StatusCode: 401, Code: "invalid_api_key"}, "Your API key was not accepted"},
		{"server error without metadata", &openai.Error{StatusCode: 502}, "HTTP 502"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := imageErrorMessage(presentation, test.err)
			if !strings.Contains(got, test.want) {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
		})
	}
}

func TestImageErrorMessageUsesInvocation(t *testing.T) {
	for _, test := range []struct{ invocation, want string }{
		{"", "openai"}, {"./openai", "./openai"}, {"'/tmp/cli with spaces/openai'", "'/tmp/cli with spaces/openai'"},
	} {
		t.Run(test.want, func(t *testing.T) {
			_, presentation := imageErrorTestContext(t, nil, test.invocation)
			apierr := imageErrorTestAPI(t, 401, "https://api.openai.com/v1/images/generations", "", "", "")
			if got := imageErrorMessage(presentation, apierr); !strings.Contains(got, test.want+" help setup") {
				t.Fatalf("missing-auth guidance lost invocation: %q", got)
			}
			presentation.saving = false
			missing := fmt.Errorf("Required flag %q not set\nRun '%s --help' for usage information", "prompt", presentation.command.FullName())
			if got := imageErrorMessage(presentation, missing); !strings.Contains(got, test.want+" images generate --prompt \"A tiny orange robot\"") {
				t.Fatalf("missing-prompt guidance lost invocation: %q", got)
			}
			if got := imageErrorMessage(presentation, errors.New("Required flag \"model\" not set")); got != "" {
				t.Fatalf("unrelated validation error replaced: %q", got)
			}
		})
	}
}

func TestImageFriendlyErrorMode(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{"default", nil, true},
		{"error JSON", []string{"--format-error", "json"}, false},
		{"error auto explicit", []string{"--format-error", "auto"}, false},
		{"response JSON", []string{"--format", "json"}, false},
		{"response auto explicit", []string{"--format", "auto"}, false},
		{"error transform", []string{"--transform-error", "code"}, false},
		{"response transform", []string{"--transform", "data"}, false},
		{"raw", []string{"--raw-output"}, false},
		{"debug", []string{"--debug"}, false},
		{"debug disabled", []string{"--debug=false"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := imageErrorTestContext(t, append(test.args, "images", "generate"), "./openai")
			if got := imageFriendlyErrorMode(root); got != test.want {
				t.Fatalf("mode = %v, want %v", got, test.want)
			}
		})
	}
}

func TestImageErrorContextDoesNotSelectUnrelatedCommands(t *testing.T) {
	for _, args := range [][]string{{"models", "list"}, {"images", "generate", "--help"}} {
		root, presentation := imageErrorTestContext(t, args, "./openai")
		if presentation != nil || root.Metadata[imageErrorContextKey] != nil {
			t.Fatalf("%q unexpectedly selected image error presentation", args)
		}
		var stderr bytes.Buffer
		if ShowFriendlyImageError(root, errors.New("synthetic-private-prompt"), &stderr) || stderr.Len() != 0 {
			t.Fatalf("%q changed unrelated output: %q", args, stderr.String())
		}
	}
	root, presentation := imageErrorTestContext(t, nil, "./openai")
	if root.Metadata[imageErrorContextKey] != presentation || presentation.command.FullName() != "openai images generate" {
		t.Fatal("image error context did not retain the parsed command")
	}
	var stderr bytes.Buffer
	if ShowFriendlyImageError(root, errors.New("synthetic-private-prompt"), &stderr) || stderr.Len() != 0 {
		t.Fatalf("non-terminal output changed: %q", stderr.String())
	}
}
