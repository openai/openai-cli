package custom

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Feedback and results share a terminal even though they use separate streams.
// Verify the public presentation entrypoints stop feedback before any result.
func TestModelsListLoadingStopsBeforeResult(t *testing.T) {
	for _, mode := range []string{"response", "iterator", "zero", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			stopped := false
			ctx := context.WithValue(t.Context(), modelsListLoadingKey{}, func() { stopped = true })
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
	}
}

func TestModelsListLoadingDoesNotStopForAnotherOperation(t *testing.T) {
	stopped := false
	ctx := context.WithValue(t.Context(), modelsListLoadingKey{}, func() { stopped = true })
	var out bytes.Buffer
	err := ShowJSON(gjson.Parse(`{"object":"file","id":"synthetic-file"}`), ShowJSONOpts{
		Context: ctx, Operation: "(resource) files > (method) retrieve", Format: "json", Stdout: &out,
	})
	require.NoError(t, err)
	require.False(t, stopped)
	require.Contains(t, out.String(), "synthetic-file")
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
