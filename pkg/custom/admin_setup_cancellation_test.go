package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAdminSetupCancellationPreservesCanonicalCause(t *testing.T) {
	privateAPI := &openai.Error{StatusCode: http.StatusUnauthorized, Message: "synthetic-private-api-cause"}
	privateURL := &url.Error{Op: "Get", URL: "https://synthetic.invalid/?key=synthetic-private-key", Err: context.Canceled}
	for _, test := range []struct {
		name        string
		cause       error
		deadline    bool
		active      bool
		wantCause   error
		wantCode    int
		wantMessage string
	}{
		{name: "keyboard cancellation", active: true, wantCause: context.Canceled, wantCode: 130, wantMessage: "Admin setup canceled. No key was saved."},
		{name: "parent cancellation", cause: context.Canceled, wantCause: context.Canceled, wantCode: 130, wantMessage: "Admin setup canceled. No key was saved."},
		{name: "parent deadline", deadline: true, wantCause: context.DeadlineExceeded, wantCode: 1, wantMessage: "Admin setup timed out. No key was saved."},
		{name: "SIGTERM cause mapping", cause: errAdminSetupTerminated, wantCause: context.Canceled, wantCode: 143, wantMessage: "Admin setup canceled. No key was saved."},
		{name: "private API parent cause", cause: privateAPI, wantCause: context.Canceled, wantCode: 130, wantMessage: "Admin setup canceled. No key was saved."},
		{name: "private URL deadline cause", cause: privateURL, deadline: true, wantCause: context.DeadlineExceeded, wantCode: 1, wantMessage: "Admin setup timed out. No key was saved."},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.Context()
			if test.deadline {
				var cancel context.CancelFunc
				parent, cancel = context.WithDeadlineCause(parent, time.Now().Add(-time.Second), test.cause)
				defer cancel()
			} else if !test.active {
				var cancel context.CancelCauseFunc
				parent, cancel = context.WithCancelCause(parent)
				cancel(test.cause)
			}
			// Exercise parent propagation and signal-handler cleanup without sending
			// process-wide signals. Actual signal delivery uses the public PTY tests.
			ctx, stop := adminSetupSignalContext(parent)
			defer stop()
			err := adminSetupCanceled(ctx)
			require.EqualError(t, err, test.wantMessage)
			require.ErrorIs(t, err, test.wantCause)
			require.Equal(t, test.wantCause, errors.Unwrap(err))
			require.Nil(t, errors.Unwrap(errors.Unwrap(err)))
			if test.wantCause == context.DeadlineExceeded {
				require.NotErrorIs(t, err, context.Canceled)
			} else {
				require.NotErrorIs(t, err, context.DeadlineExceeded)
			}
			var exit cli.ExitCoder
			require.ErrorAs(t, err, &exit)
			require.Equal(t, test.wantCode, exit.ExitCode())
			var apiError *openai.Error
			var urlError *url.Error
			require.False(t, errors.As(err, &apiError))
			require.False(t, errors.As(err, &urlError))
			require.NotErrorIs(t, err, privateAPI)
			require.NotErrorIs(t, err, privateURL)
			assertAdminSetupCancellationPresentation(t, err, test.wantMessage)
		})
	}
}

func TestAdminSetupVerificationCancellationPreservesCause(t *testing.T) {
	for _, name := range []string{"OPENAI_BASE_URL", "OPENAI_CUSTOM_HEADERS", "OPENAI_API_KEY", "OPENAI_ADMIN_KEY"} {
		t.Setenv(name, "")
	}
	for _, name := range []string{"parent cancellation", "parent deadline", "internal timeout"} {
		t.Run(name, func(t *testing.T) {
			// A controlled transport avoids network activity. Virtual time exercises
			// the actual 15-second deadline without delaying the test suite.
			synctest.Test(t, func(t *testing.T) {
				parent := context.Background()
				var cancel context.CancelCauseFunc
				wantCause := error(context.DeadlineExceeded)
				wantCode := 1
				wantMessage := "Verification timed out after 15 seconds. Check your connection, then run setup admin again."
				if name == "parent cancellation" {
					parent, cancel = context.WithCancelCause(parent)
					defer cancel(nil)
					wantCause, wantCode = context.Canceled, 130
					wantMessage = "Admin setup canceled. No key was saved."
				} else if name == "parent deadline" {
					var stop context.CancelFunc
					parent, stop = context.WithTimeout(parent, time.Second)
					defer stop()
					wantMessage = "Admin setup timed out. No key was saved."
				}
				requests := 0
				client := &http.Client{Transport: adminSetupCancellationTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, "GET", request.Method)
					require.Equal(t, "/v1/organization/projects?limit=1", request.URL.RequestURI())
					if cancel != nil {
						cancel(&openai.Error{Message: "synthetic-private-parent-cause"})
					}
					<-request.Context().Done()
					return nil, &url.Error{Op: "Get", URL: "https://synthetic.invalid/?key=synthetic-private-key", Err: request.Context().Err()}
				})}
				err := verifyAdminSetup(parent, []option.RequestOption{
					option.WithBaseURL("http://127.0.0.1/v1"), option.WithHTTPClient(client), option.WithMaxRetries(0),
				}, []byte("synthetic-admin-key"))
				require.Equal(t, 1, requests)
				require.EqualError(t, err, wantMessage)
				require.ErrorIs(t, err, wantCause)
				require.Equal(t, wantCause, errors.Unwrap(err))
				var exit cli.ExitCoder
				require.ErrorAs(t, err, &exit)
				require.Equal(t, wantCode, exit.ExitCode())
				var apiError *openai.Error
				var urlError *url.Error
				require.False(t, errors.As(err, &apiError))
				require.False(t, errors.As(err, &urlError))
				assertAdminSetupCancellationPresentation(t, err, wantMessage)
			})
		})
	}
}

type adminSetupCancellationTransport func(*http.Request) (*http.Response, error)

func (transport adminSetupCancellationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func assertAdminSetupCancellationPresentation(t *testing.T, failure error, message string) {
	t.Helper()
	for _, format := range []string{"text", "json"} {
		root := readableErrorTestCommand(t, "--format-error", format)
		var out bytes.Buffer
		// The normal command wrapper must retain the canonical cause and code.
		wrapped := withCommandError(root, failure)
		require.NoError(t, ShowCommandError(root, wrapped, &out))
		assertReadableErrorContainsNoPrivateDetails(t, out.String())
		if format == "text" {
			require.Equal(t, message+"\n", out.String())
		} else {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(out.Bytes(), &payload))
			require.Equal(t, map[string]any{"message": message}, payload)
		}
	}
}
