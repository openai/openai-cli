package custom

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/commands/models"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Feedback and results share a terminal even though they use separate streams.
// Verify the public presentation entrypoints stop feedback before any result.
func TestModelsListLoadingStopsBeforeResult(t *testing.T) {
	for _, mode := range []string{"response", "iterator", "zero", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			stopped := false
			withModelFeedback(t, func() { stopped = true }, func(ctx context.Context) {
				out := &modelsLoadingResultWriter{t: t, stopped: &stopped}
				opts := ShowJSONOpts{Context: ctx, Operation: "(resource) models > (method) list", Format: "json", Stdout: out}
				item := gjson.Parse(`{"object":"model","id":"synthetic-model","owned_by":"synthetic-owner"}`)
				switch mode {
				case "response":
					require.NoError(t, ShowJSON(item, opts))
				case "canceled":
					ctx, cancel := context.WithCancel(ctx)
					cancel()
					opts.Context = ctx
					require.ErrorIs(t, ShowJSON(item, opts), context.Canceled)
					require.Empty(t, out.String())
				default:
					source, _ := staticModelIterator(t, "["+item.Raw+"]", -1, nil)
					maximum := int64(-1)
					if mode == "zero" {
						maximum = 0
					}
					require.NoError(t, ShowJSONIterator(source, maximum, opts))
				}
				require.True(t, stopped)
				if mode == "response" || mode == "iterator" {
					require.Contains(t, out.String(), "synthetic-model")
					require.Contains(t, out.String(), "synthetic-owner")
				}
			})
		})
	}
}

func TestModelsListLoadingDoesNotStopForAnotherOperation(t *testing.T) {
	stopped := false
	withModelFeedback(t, func() { stopped = true }, func(ctx context.Context) {
		var out bytes.Buffer
		err := ShowJSON(gjson.Parse(`{"object":"file","id":"synthetic-file"}`), ShowJSONOpts{
			Context: ctx, Operation: "(resource) files > (method) retrieve", Format: "json", Stdout: &out,
		})
		require.NoError(t, err)
		require.False(t, stopped)
		require.Contains(t, out.String(), "synthetic-file")
	})
}

type modelsLoadingResultWriter struct {
	buffer  bytes.Buffer
	t       *testing.T
	stopped *bool
}

func (w *modelsLoadingResultWriter) Write(data []byte) (int, error) {
	w.t.Helper()
	require.True(w.t, *w.stopped, "result writes must follow feedback cleanup")
	return w.buffer.Write(data)
}

func (w *modelsLoadingResultWriter) String() string { return w.buffer.String() }

func withModelFeedback(t *testing.T, stop func(), action func(context.Context)) {
	t.Helper()
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{Name: "list",
			Action: func(ctx context.Context, _ *cli.Command) error { action(ctx); return nil },
		}}}},
	}
	models.Configure(root, models.Options{
		LoadingAllowed: func(*cli.Command) bool { return true },
		StartFeedback:  func(context.Context, *cli.Command) func() { return stop },
	})
	require.NoError(t, root.Run(t.Context(), []string{"openai", "models", "list"}))
}
