package batches

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func TestBatchBrowserTerminalFailureStopsNavigation(t *testing.T) {
	interrupt := &WorkflowError{message: "Synthetic terminal interruption", code: 130, cause: context.Canceled}
	ended := &WorkflowError{message: "Synthetic terminal input ended", code: 1}
	for _, screen := range []string{"actions", "path", "document"} {
		for _, previous := range []string{"none", "local", "api"} {
			for name, terminalErr := range map[string]error{"interrupt": interrupt, "ended": ended, "output": io.ErrClosedPipe} {
				t.Run(screen+"/"+previous+"/"+name, func(t *testing.T) {
					gets := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/batches" {
							_, _ = io.WriteString(w, `{"data":[{"id":"batch_one"}],"has_more":false}`)
							return
						}
						require.Equal(t, "/batches/batch_one", r.URL.Path)
						gets++
						if gets == 1 && previous == "local" {
							_, _ = io.WriteString(w, `{"id":"unexpected_batch"}`)
							return
						}
						if gets == 1 && previous == "api" {
							w.WriteHeader(http.StatusForbidden)
							_, _ = io.WriteString(w, `{"error":{"message":"synthetic denied","type":"permission_error"}}`)
							return
						}
						_, _ = io.WriteString(w, `{"id":"batch_one","status":"completed","output_file_id":"file_one"}`)
					}))
					defer server.Close()
					failureReviewed := previous == "none"
					stopped := false
					var screens []string
					show := func(ctx context.Context, m *batchBrowserScreen) error {
						require.NoError(t, ctx.Err(), "nested terminal errors do not cancel the controller context")
						require.False(t, stopped, "terminal failure must not open another screen: %s", m.kind)
						screens = append(screens, m.kind)
						if !failureReviewed {
							if m.kind == "document" {
								require.Equal(t, "Action failed", m.title)
								failureReviewed = true
								m.chosen = "back"
								return nil
							}
						} else if m.kind == screen {
							stopped = true
							return fmt.Errorf("terminal screen: %w", terminalErr)
						}
						switch m.kind {
						case "list":
							m.chosen = "batch:batch_one"
						case "actions":
							m.chosen = "save:output"
							if !failureReviewed || screen == "document" {
								m.chosen = "status"
							}
						default:
							t.Fatalf("unexpected screen %s", m.kind)
						}
						return nil
					}
					root := browserTestCommand(t, server.URL, io.Discard, show)
					err := root.Run(t.Context(), []string{"openai", "batches", "browse"})
					require.True(t, stopped)
					require.Equal(t, screen, screens[len(screens)-1])
					require.ErrorIs(t, err, terminalErr)
					require.NoError(t, t.Context().Err())
					if name == "interrupt" {
						require.ErrorIs(t, err, context.Canceled)
						var exit interface{ ExitCode() int }
						require.ErrorAs(t, err, &exit)
						require.Equal(t, 130, exit.ExitCode())
					}
					if previous == "api" {
						var apierr *openai.Error
						require.ErrorAs(t, err, &apierr, "prior API failure must remain inspectable")
						require.Equal(t, http.StatusForbidden, apierr.StatusCode)
					}
					if previous == "local" {
						require.Contains(t, err.Error(), "invalid Batch details", "prior local failure must remain inspectable")
					}
				})
			}
		}
	}
}
