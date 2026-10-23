package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openai/openai-cli/internal/commands/webhooks"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestReadableErrorCancellationTimeoutAndConnection(t *testing.T) {
	const privateURL = "https://synthetic.example.invalid/?token=synthetic-secret"
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"canceled context", context.Canceled, "Request canceled.\n"},
		{"canceled request", &url.Error{Op: "Post", URL: privateURL, Err: context.Canceled}, "Request canceled.\n"},
		{"wrapped cancellation", fmt.Errorf("synthetic-private-prompt: %w", &url.Error{Op: "Post", URL: privateURL, Err: context.Canceled}), "Request canceled.\n"},
		{"joined cancellation", errors.Join(errors.New("synthetic-secret"), context.Canceled), "Request canceled.\n"},
		{"deadline", &url.Error{Op: "Post", URL: privateURL, Err: context.DeadlineExceeded}, "The request timed out."},
		{"network timeout", &url.Error{Op: "Post", URL: privateURL, Err: &net.DNSError{Name: "synthetic-private-prompt", IsTimeout: true}}, "The request timed out."},
		{"connection", &url.Error{Op: "Post", URL: privateURL, Err: errors.New("synthetic-private-prompt \x1b]52;c;synthetic-secret\a")}, "Could not connect to the API."},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := readableErrorTestCommand(t)
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(root, test.err, &out))
			require.Contains(t, out.String(), test.want)
			assertReadableErrorContainsNoPrivateDetails(t, out.String())
			if strings.Contains(test.name, "cancel") {
				require.Equal(t, test.want, out.String())
			} else if test.name != "connection" {
				require.Contains(t, out.String(), "check its status before repeating it")
			}
		})
	}
}

func TestReadableAPIErrorSummaryDoesNotEchoResponseDetails(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		code   string
		want   string
	}{
		{"authentication", http.StatusUnauthorized, "invalid_api_key", "Key setup: ./openai help setup"},
		{"permissions", http.StatusForbidden, "synthetic-private-code", "Check your key's permissions."},
		{"not found", http.StatusNotFound, "synthetic-private-code", "Check the resource or model ID.\nCheck your key's access to it."},
		{"invalid argument", http.StatusBadRequest, "synthetic-private-code", "The API rejected the request."},
		{"unprocessable argument", http.StatusUnprocessableEntity, "synthetic-private-code", "The API rejected the request."},
		{"quota", http.StatusTooManyRequests, "insufficient_quota", "API usage or billing limit reached."},
		{"rate", http.StatusTooManyRequests, "synthetic-private-code", "Wait before trying again."},
		{"conflict", http.StatusConflict, "synthetic-private-code", "Check its current status."},
		{"request timeout", http.StatusRequestTimeout, "synthetic-private-code", "Check its status before trying again."},
		{"gateway timeout", http.StatusGatewayTimeout, "synthetic-private-code", "Check its status before trying again."},
		{"server", http.StatusInternalServerError, "synthetic-private-code", "The API is temporarily unavailable."},
		{"unknown status", 499, "synthetic-private-code", "The API could not complete the request."},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := readableErrorTestCommand(t)
			request, err := http.NewRequest(http.MethodPost, "https://synthetic.example.invalid/?token=synthetic-secret", nil)
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer synthetic-secret")
			apierr := &openai.Error{
				StatusCode: test.status, Code: test.code, Request: request,
				Message: "synthetic-private-prompt https://synthetic.example.invalid/?token=synthetic-secret \x1b]52;c;synthetic-secret\a\u202e",
				Param:   "synthetic-private-param",
			}
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(root, fmt.Errorf("synthetic-wrapper: %w", apierr), &out))
			require.Contains(t, out.String(), test.want)
			require.Contains(t, out.String(), fmt.Sprintf("HTTP %d", test.status))
			require.Contains(t, out.String(), "--format-error json")
			assertReadableErrorContainsNoPrivateDetails(t, out.String())
			if test.status == http.StatusUnauthorized {
				require.Contains(t, out.String(), "./openai help setup")
			}
		})
	}
}

func TestReadableAPIErrorHeaderHandlesUnknownOrMissingStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		header string
	}{
		{http.StatusInternalServerError, "HTTP 500: Internal Server Error.\n"},
		{599, "HTTP 599.\n"},
		{0, "The API could not complete the request.\n"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(readableErrorTestCommand(t), &openai.Error{StatusCode: tc.status}, &out))
			require.True(t, strings.HasPrefix(out.String(), tc.header), out.String())
			require.NotContains(t, out.String(), "HTTP 0")
			require.NotContains(t, out.String(), ": .")
		})
	}
}

func TestReadableErrorFormatRouting(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		format  string
		summary bool
	}{
		{"default", nil, "text", true},
		{"explicit auto", []string{"--format", "auto"}, "text", true},
		{"explicit text", []string{"--format", "text"}, "text", true},
		{"success extraction", []string{"--transform", "data.0"}, "text", true},
		{"success raw output", []string{"--raw-output"}, "text", true},
		{"pretty success", []string{"--format", "pretty"}, "text", true},
		{"explore success", []string{"--format", "explore"}, "text", true},
		{"JSON errors with JSON output", []string{"--format", "json"}, "json", false},
		{"JSONL errors with JSONL output", []string{"--format", "jsonl"}, "jsonl", false},
		{"raw errors with raw output", []string{"--format", "raw"}, "raw", false},
		{"YAML errors with YAML output", []string{"--format", "yaml"}, "yaml", false},
		{"case normalization", []string{"--format", "JSON"}, "json", false},
		{"text error override", []string{"--format", "json", "--format-error", "text"}, "text", true},
		{"auto error override", []string{"--format", "json", "--format-error", "auto"}, "text", true},
		{"JSON error override", []string{"--format", "text", "--format-error", "json"}, "json", false},
		{"JSON error case override", []string{"--format", "yaml", "--format-error", "JSON"}, "json", false},
		{"error extraction", []string{"--transform-error", "message"}, "json", false},
		{"auto error extraction", []string{"--format", "yaml", "--format-error", "auto", "--transform-error", "message"}, "json", false},
		{"text error extraction", []string{"--format-error", "text", "--transform-error", "message"}, "text", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := readableErrorTestCommand(t, test.args...)
			require.Equal(t, test.format, errorOutputFormat(root))
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(root, &openai.Error{StatusCode: http.StatusBadRequest}, &out))
			if test.summary {
				require.Contains(t, out.String(), "The API rejected the request.")
			} else {
				require.NotContains(t, out.String(), "The API rejected the request.")
			}
		})
	}
}

type errorSink struct{ err error }

func (w errorSink) Write([]byte) (int, error) { return 0, w.err }

type shortErrorSink struct{}

func (shortErrorSink) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestCommandErrorPreservesWriterFailures(t *testing.T) {
	cause := errors.New("synthetic writer failure")
	apierr := &openai.Error{StatusCode: http.StatusBadRequest}
	require.NoError(t, json.Unmarshal([]byte(`{"message":"synthetic response","code":"invalid_value"}`), apierr))
	streamerr := &ssestream.StreamError{Event: ssestream.Event{Data: []byte(`{"error":{"message":"synthetic stream failure"}}`)}}
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		for _, failure := range []error{apierr, streamerr, errors.New("synthetic local failure")} {
			t.Run(format+fmt.Sprintf("/%T", failure), func(t *testing.T) {
				root := readableErrorTestCommand(t, "--format-error", format)
				require.ErrorIs(t, ShowCommandError(root, failure, errorSink{cause}), cause)
				require.ErrorIs(t, ShowCommandError(root, failure, shortErrorSink{}), io.ErrShortWrite)
			})
		}
	}
}

func TestCommandErrorStructuredLocalFallbackDoesNotEchoDiagnostic(t *testing.T) {
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			root := readableErrorTestCommand(t, "--format-error", format)
			var out bytes.Buffer
			err := errors.New("synthetic-private-prompt https://synthetic.invalid/?token=synthetic-secret \x1b]52;c;synthetic-secret\a\u202e")
			require.NoError(t, ShowCommandError(root, err, &out))
			assertReadableErrorContainsNoPrivateDetails(t, out.String())
			require.Contains(t, out.String(), "The command could not be completed.")
		})
	}
}

func TestCommandErrorInvalidFormatStillPresentsSafely(t *testing.T) {
	root := readableErrorTestCommand(t, "--format-error", "synthetic-secret")
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(root, errors.New("synthetic-secret"), &out))
	assertReadableErrorContainsNoPrivateDetails(t, out.String())
	require.NotEmpty(t, out.String())
}

func readableErrorTestCommand(t *testing.T, args ...string) *cli.Command {
	t.Helper()
	root := &cli.Command{
		Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, HideHelpCommand: true,
		Metadata: map[string]any{"help-invocation": "./openai"},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "auto"},
			&cli.StringFlag{Name: "transform-error"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
	require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, args...)))
	return root
}

func TestReadableModelGuidanceRequiresDeclaredFlag(t *testing.T) {
	apierr := &openai.Error{StatusCode: http.StatusBadRequest, Code: "model_not_found", Param: "model"}
	for _, hasModel := range []bool{false, true} {
		command := &cli.Command{Name: "retrieve"}
		if hasModel {
			command.Flags = []cli.Flag{&requestflag.Flag[string]{Name: "model", PathParam: "model"}}
		}
		message := readableAPIArgumentMessage(apierr, command)
		require.Equal(t, hasModel, strings.Contains(message, "--model"))
	}
}

func assertReadableErrorContainsNoPrivateDetails(t *testing.T, text string) {
	t.Helper()
	// Fixed public recovery links are permitted. Arbitrary response/request
	// URLs and credential-bearing query parameters remain forbidden.
	for _, public := range []string{
		"https://platform.openai.com/settings/organization/api-keys",
		"https://developers.openai.com/api/docs/guides/ip-allowlist",
	} {
		text = strings.ReplaceAll(text, public, "")
	}
	for _, private := range []string{"synthetic-", "https://", "token=", "\x1b", "\a", "\u202e"} {
		require.NotContains(t, text, private)
	}
}

func TestReadableStreamErrorSummaryAndCancellation(t *testing.T) {
	streamerr := &ssestream.StreamError{
		Message: "synthetic-private-prompt",
		Event:   ssestream.Event{Data: []byte(`{"error":{"message":"synthetic-private-prompt","code":"synthetic-private-code"}}`)},
	}
	for _, test := range []struct {
		name    string
		failure error
		flags   []string
		want    string
	}{
		{"wrapped stream", fmt.Errorf("synthetic-private-wrapper: %w", streamerr), nil, "The response stream failed.\nOutput may be incomplete."},
		{"canceled stream", errors.Join(streamerr, context.Canceled), nil, "Request canceled.\n"},
		{"canceled structured stream", errors.Join(streamerr, context.Canceled), []string{"--format-error", "json"}, "Request canceled."},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(readableErrorTestCommand(t, test.flags...), test.failure, &out))
			require.Contains(t, out.String(), test.want)
			assertReadableErrorContainsNoPrivateDetails(t, out.String())
			require.NotContains(t, out.String(), "status_code")
			if test.name == "wrapped stream" {
				require.Contains(t, out.String(), "--format-error json")
			}
		})
	}
}

func TestStructuredStreamErrorWithoutJSONData(t *testing.T) {
	for _, data := range []string{"", "null", "synthetic-private-data", `{"error":`} {
		t.Run(data, func(t *testing.T) {
			var out bytes.Buffer
			failure := &ssestream.StreamError{Message: "synthetic-private-prompt", Event: ssestream.Event{Data: []byte(data)}}
			require.NoError(t, ShowCommandError(readableErrorTestCommand(t, "--format-error", "json"), failure, &out))
			var payload map[string]any
			require.NoError(t, json.Unmarshal(out.Bytes(), &payload))
			require.Equal(t, "The response stream failed. Output may be incomplete.\nFor API error details, add --format-error json.", payload["message"])
			require.NotContains(t, payload, "status_code")
			assertReadableErrorContainsNoPrivateDetails(t, out.String())
		})
	}
}

func TestWebhookDiscoveryErrorPreservesAPIFormatsAndKnownOutcome(t *testing.T) {
	apierr := &openai.Error{StatusCode: http.StatusUnauthorized}
	require.NoError(t, json.Unmarshal([]byte(`{"message":"synthetic-private-response","code":"invalid_api_key","future_field":true}`), apierr))
	failure := &webhooks.WorkflowError{Message: "Could not load webhook event types.\nNo endpoint was created.", Cause: apierr}
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			root := readableErrorTestCommand(t, "--format-error", format)
			var before, after bytes.Buffer
			require.NoError(t, ShowCommandError(root, apierr, &before))
			require.NoError(t, ShowCommandError(root, failure, &after))
			if format == "auto" || format == "text" {
				require.Contains(t, after.String(), "Could not load webhook event types.\nNo endpoint was created.")
				require.NotContains(t, after.String(), "synthetic-private-response")
				require.NotContains(t, before.String(), "No endpoint was created")
			} else {
				require.Equal(t, before.String(), after.String())
			}
		})
	}
	root := readableErrorTestCommand(t, "--transform-error", "future_field")
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(root, failure, &out))
	require.Equal(t, "true\n", out.String())
	require.ErrorIs(t, failure, apierr)
}

func TestWebhookCreateAPIFailurePreservesFormatsAndIdentity(t *testing.T) {
	apierr := &openai.Error{StatusCode: http.StatusInternalServerError}
	require.NoError(t, json.Unmarshal([]byte(`{"message":"synthetic-private-response","code":"server_error","future_field":true}`), apierr))
	// Presenter fixture has no invocation context. Feature and public-command tests cover wrapping.
	failure := &webhooks.WorkflowError{Message: "Webhook creation is unconfirmed. The endpoint may already exist.\nBefore repeating create, check the endpoint list for the requested name and URL.\nUse webhooks list with your original authentication, project, organization, and API settings.", Cause: apierr}
	var underlying *openai.Error
	require.ErrorIs(t, failure, apierr)
	require.ErrorAs(t, failure, &underlying)
	require.Same(t, apierr, underlying)
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			root := readableErrorTestCommand(t, "--format-error", format)
			var before, after bytes.Buffer
			require.NoError(t, ShowCommandError(root, apierr, &before))
			require.NoError(t, ShowCommandError(root, failure, &after))
			if format == "auto" || format == "text" {
				require.Contains(t, after.String(), "Webhook creation is unconfirmed.")
				require.Contains(t, after.String(), "webhooks list with your original authentication, project, organization, and API settings")
				require.NotContains(t, after.String(), "synthetic-private-response")
			} else {
				require.Equal(t, before.String(), after.String())
			}
		})
	}
	root := readableErrorTestCommand(t, "--transform-error", "future_field")
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(root, failure, &out))
	require.Equal(t, "true\n", out.String())
}

func TestWebhookCreateTransportFailurePreservesFallbackAndIdentity(t *testing.T) {
	failure := &url.Error{Op: "Post", URL: "https://example.invalid/?token=synthetic-private", Err: io.EOF}
	for _, flags := range [][]string{nil, {"--format-error", "text"},
		{"--format", "json", "--format-error", "text"}, {"--format", "json"},
		{"--format-error", "json"}, {"--format-error", "jsonl"}, {"--format-error", "raw"},
		{"--format-error", "yaml"}, {"--transform-error", "message"},
		{"--format-error", "text", "--transform-error", "message"},
	} {
		root := readableErrorTestCommand(t, flags...)
		got := wrapWebhookAppCreateError(t.Context(), root, failure)
		require.ErrorIs(t, got, failure)
		var transport *url.Error
		require.ErrorAs(t, got, &transport)
		require.Same(t, failure, transport)
		var before, after bytes.Buffer
		require.NoError(t, ShowCommandError(root, failure, &before))
		require.NoError(t, ShowCommandError(root, got, &after))
		if errorOutputFormat(root) == "text" && root.String("transform-error") == "" {
			require.Contains(t, after.String(), before.String())
			require.Contains(t, after.String(), "Webhook creation is unconfirmed.")
			require.Contains(t, after.String(), "webhooks list")
			require.NotContains(t, after.String(), "synthetic-private")
		} else {
			require.Same(t, failure, got)
			require.Equal(t, before.String(), after.String())
		}
	}
	timeout := &url.Error{Op: "Post", URL: "https://example.invalid/?token=synthetic-private", Err: context.DeadlineExceeded}
	for _, flags := range [][]string{nil, {"--format-error", "json"}, {"--transform-error", "message"}} {
		root := readableErrorTestCommand(t, flags...)
		got := wrapWebhookAppCreateError(t.Context(), root, timeout)
		require.ErrorIs(t, got, context.DeadlineExceeded)
		var before, after bytes.Buffer
		require.NoError(t, ShowCommandError(root, timeout, &before))
		require.NoError(t, ShowCommandError(root, got, &after))
		if len(flags) == 0 {
			require.Contains(t, after.String(), before.String())
			require.Contains(t, after.String(), "The request timed out.")
			require.Contains(t, after.String(), "webhooks list")
			require.NotContains(t, after.String(), "synthetic-private")
		} else {
			require.Same(t, timeout, got)
			require.Equal(t, before.String(), after.String())
		}
	}
	root := readableErrorTestCommand(t)
	for _, unchanged := range []error{
		&url.Error{Op: "Get", Err: io.EOF},
		&url.Error{Op: "Post", Err: context.Canceled},
		&url.Error{Op: "Post", Err: errors.New("mTLS does not support HTTPS proxies")},
		errors.New("mTLS client certificate and key files must be configured together"),
	} {
		require.Same(t, unchanged, wrapWebhookAppCreateError(t.Context(), root, unchanged))
	}
}

func TestWebhookRecoveryErrorsPreserveCausesAndPrivacy(t *testing.T) {
	private := "private-home-secret-token"
	for _, cause := range []error{
		&os.PathError{Op: "write", Path: "/" + private, Err: os.ErrPermission},
		&os.LinkError{Op: "link", Old: "/" + private, New: "/other-private-path", Err: os.ErrPermission},
		errors.Join(&os.PathError{Op: "write", Path: "/" + private, Err: os.ErrPermission}, errors.New(private)),
		context.DeadlineExceeded,
	} {
		failure := &webhooks.WorkflowError{Message: "Webhook creation did not finish cleanly. The endpoint may already exist. Use webhooks list with your original authentication, project, organization, and API settings before repeating create.", Cause: cause}
		for _, wrapped := range []error{failure, fmt.Errorf("%s: %w", private, failure), errors.Join(errors.New(private), failure)} {
			message := localErrorMessage(&cli.Command{}, wrapped)
			if !errors.Is(wrapped, cause) || strings.Contains(message, private) ||
				!strings.Contains(message, "original authentication, project, organization, and API settings") ||
				!strings.Contains(message, "before repeating create") {
				t.Fatalf("unsafe or incomplete recovery: %q", message)
			}
			var output bytes.Buffer
			if err := ShowCommandError(readableErrorTestCommand(t), wrapped, &output); err != nil ||
				strings.Contains(output.String(), private) || !strings.Contains(output.String(), message) {
				t.Fatalf("presenter lost safe recovery: error=%v output=%q", err, output.String())
			}
		}
	}
	canceled := errors.Join(context.Canceled, &webhooks.WorkflowError{Message: "Creation may exist.", Cause: os.ErrPermission})
	if got := localErrorMessage(&cli.Command{}, canceled); got != "Request canceled." {
		t.Fatalf("cancellation lost precedence: %q", got)
	}
}

func TestWebhookSubmittedCancellationUsesExistingErrorPresenter(t *testing.T) {
	const private = "synthetic-private-path-token"
	cause := errors.Join(context.Canceled, &os.PathError{Op: "write", Path: private, Err: os.ErrPermission})
	failure := &webhooks.CreateCanceledError{Cause: cause}
	for _, format := range []string{"text", "json", "jsonl"} {
		for _, wrapped := range []error{failure, fmt.Errorf("%s: %w", private, failure), errors.Join(errors.New(private), failure)} {
			var out bytes.Buffer
			if err := ShowCommandError(readableErrorTestCommand(t, "--format-error", format), wrapped, &out); err != nil {
				t.Fatal(err)
			}
			message := strings.TrimSpace(out.String())
			if format != "text" {
				var value struct{ Message string }
				if err := json.Unmarshal(out.Bytes(), &value); err != nil {
					t.Fatalf("structured cancellation was polluted: %v, %q", err, out.String())
				}
				message = value.Message
			}
			if message != failure.Error() || strings.Contains(out.String(), private) || !errors.Is(wrapped, context.Canceled) {
				t.Fatalf("submitted cancellation lost safe recovery: %q", out.String())
			}
		}
	}
}

// Exercise the public feature decorator while retaining app-owned presenter assertions.
func wrapWebhookAppCreateError(ctx context.Context, root *cli.Command, failure error) error {
	if root == nil {
		root = &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard}
	}
	create := &cli.Command{Name: "create", Action: func(context.Context, *cli.Command) error { return failure }}
	container := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "webhooks", Commands: []*cli.Command{create}}}}
	webhooks.Configure(container, webhookCommandOptions())
	return create.Action(ctx, root)
}
