package custom

import (
	"bytes"
	"context"
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
		{"deadline", &url.Error{Op: "Post", URL: privateURL, Err: context.DeadlineExceeded}, "The request timed out."},
		{"network timeout", &url.Error{Op: "Post", URL: privateURL, Err: &net.DNSError{Name: "synthetic-private-prompt", IsTimeout: true}}, "The request timed out."},
		{"connection", &url.Error{Op: "Post", URL: privateURL, Err: errors.New("synthetic-private-prompt \x1b]52;c;synthetic-secret\a")}, "Could not connect to the API."},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := readableErrorTestCommand(t)
			var out bytes.Buffer
			require.True(t, ShowReadableError(root, test.err, &out))
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
		{"authentication", http.StatusUnauthorized, "invalid_api_key", "Authentication failed."},
		{"invalid argument", http.StatusBadRequest, "synthetic-private-code", "The API rejected the request."},
		{"quota", http.StatusTooManyRequests, "insufficient_quota", "API usage or billing limit reached."},
		{"server", http.StatusInternalServerError, "synthetic-private-code", "The API is temporarily unavailable."},
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
			require.True(t, ShowReadableError(root, fmt.Errorf("synthetic-wrapper: %w", apierr), &out))
			require.Contains(t, out.String(), test.want)
			require.Contains(t, out.String(), fmt.Sprintf("Request failed (%d", test.status))
			require.Contains(t, out.String(), "--format-error json")
			assertReadableErrorContainsNoPrivateDetails(t, out.String())
			if test.status == http.StatusUnauthorized {
				require.Contains(t, out.String(), "./openai help setup")
			}
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
		{"JSON errors with JSON output", []string{"--format", "json"}, "json", false},
		{"JSONL errors with JSONL output", []string{"--format", "jsonl"}, "jsonl", false},
		{"raw errors with raw output", []string{"--format", "raw"}, "raw", false},
		{"YAML errors with YAML output", []string{"--format", "yaml"}, "yaml", false},
		{"text error override", []string{"--format", "json", "--format-error", "text"}, "text", true},
		{"auto error override", []string{"--format", "json", "--format-error", "auto"}, "text", true},
		{"JSON error override", []string{"--format", "text", "--format-error", "json"}, "json", false},
		{"error extraction", []string{"--transform-error", "message"}, "json", false},
		{"text error extraction", []string{"--format-error", "text", "--transform-error", "message"}, "text", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := readableErrorTestCommand(t, test.args...)
			require.Equal(t, test.format, ErrorOutputFormat(root))
			var out bytes.Buffer
			require.Equal(t, test.summary, ShowReadableError(root, &openai.Error{StatusCode: http.StatusBadRequest}, &out))
			if test.summary {
				require.Contains(t, out.String(), "The API rejected the request.")
			} else {
				require.Empty(t, out.String(), "explicit data formatting must retain ownership of the error")
			}
		})
	}
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
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
	require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, args...)))
	return root
}

func assertReadableErrorContainsNoPrivateDetails(t *testing.T, text string) {
	t.Helper()
	for _, private := range []string{"synthetic-", "https://", "token=", "\x1b", "\a", "\u202e"} {
		require.NotContains(t, text, private)
	}
}

func TestReadableParameterFlagUsesExactCommandPaths(t *testing.T) {
	messages := &requestflag.Flag[[]map[string]any]{Name: "message", BodyPath: "messages"}
	command := &cli.Command{Flags: []cli.Flag{
		messages,
		&requestflag.InnerFlag[string]{Name: "message.content", OuterFlag: messages, InnerField: "content"},
		&requestflag.Flag[string]{Name: "model", PathParam: "model"},
		&requestflag.Flag[int64]{Name: "page-size", QueryPath: "limit"},
		&cli.StringFlag{Name: "format"},
	}}
	for _, test := range []struct{ param, flag, path string }{
		{"messages[0].content", "--message.content", "messages.content"},
		{"messages.1.content", "--message.content", "messages.content"},
		{"messages.1.0.content", "--message.content", "messages.content"},
		{"messages[2].future_field", "--message", "messages"},
		{"messages[2]", "--message", "messages"},
		{"model", "--model", "model"},
		{"limit", "--page-size", "limit"},
		{"format", "", ""},
		{"messages_evil", "", ""},
		{"messages.0.content\x1b[2J", "", ""},
		{"messages['synthetic-secret']", "", ""},
		{"https://synthetic.invalid/?token=secret", "", ""},
	} {
		t.Run(test.param, func(t *testing.T) {
			name, path := readableParameterFlag(command, test.param)
			require.Equal(t, test.flag, name)
			require.Equal(t, test.path, path)
		})
	}
}
