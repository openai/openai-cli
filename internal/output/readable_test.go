package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"

	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
)

func TestReadableIteratorFinalDestination(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		var out bytes.Buffer
		source := &transformTestIterator{items: []any{map[string]string{"value": "synthetic\x1b]52;c;data\a"}}}
		iter := NewIterator(source, IteratorOptions{Context: t.Context(), Transform: transformers.Identity, Limit: -1})
		err := WriteReadableIterator(iter, ReadableOptions{
			Context: t.Context(), FormatOptions: FormatOptions{Format: "text", RawOutput: true, Transform: "value"},
			Stdout: &out, Terminal: terminal,
		})
		require.NoError(t, err)
		want := "synthetic\x1b]52;c;data\a\n"
		if terminal {
			want = "synthetic\\u001b]52;c;data\\u0007\n"
		}
		require.Equal(t, want, out.String())
		require.Equal(t, 2, source.calls)
	}
}

func TestReadableIteratorDiagnosticPermissionAndOrigin(t *testing.T) {
	upstream := errors.New("synthetic upstream failure")
	route := transformers.Route{Operation: "(resource) files > (method) list", OutputKind: transformers.OutputPageItem}
	for _, allowed := range []bool{false, true} {
		for _, fail := range []error{nil, syscall.EPIPE} {
			var out, diagnostic bytes.Buffer
			source := &transformTestIterator{items: []any{map[string]any{"id": "file_synthetic", "object": "file", "created_at": 17}}, err: upstream}
			iter := NewIterator(source, IteratorOptions{Context: t.Context(), Transform: transformers.Identity, Route: route, Limit: -1})
			stderr := writeFunc(func(data []byte) (int, error) {
				require.Equal(t, 2, source.calls, "notice follows the complete list")
				if fail != nil {
					return 0, fail
				}
				return diagnostic.Write(data)
			})
			err := WriteReadableIterator(iter, ReadableOptions{Context: t.Context(), Route: route,
				FormatOptions: FormatOptions{Format: "text"}, Stdout: &out, Stderr: stderr, DiagnosticsAllowed: allowed})
			require.ErrorIs(t, err, upstream)
			require.Contains(t, out.String(), "ID: file_synthetic\n")
			require.NotContains(t, out.String(), ResourceSummaryHint)
			if allowed && fail != nil {
				require.ErrorIs(t, err, fail)
				var diagnosticError *DiagnosticError
				require.ErrorAs(t, err, &diagnosticError)
				require.False(t, IsBrokenPipe(err))
			} else if allowed {
				require.Equal(t, ResourceSummaryHint+"\n", diagnostic.String())
			} else {
				require.Empty(t, diagnostic.String())
				require.False(t, errors.Is(err, syscall.EPIPE))
			}
		}
	}
}

func TestReadableStreamDeliversBeforeAdvancing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := &transformTestIterator{items: []any{
		map[string]any{"type": "response.output_text.delta", "delta": "synthetic partial"},
		map[string]any{"must_not_read": true},
	}}
	route := transformers.Route{Operation: "(resource) responses > (method) create", OutputKind: transformers.OutputStreamEvent}
	iter := NewIterator(source, IteratorOptions{Context: ctx, Transform: transformers.Identity, Route: route, Limit: -1})
	var out strings.Builder
	sink := writeFunc(func(data []byte) (int, error) {
		require.Equal(t, 1, source.calls)
		n, err := io.WriteString(&out, string(data))
		cancel()
		return n, err
	})
	err := WriteReadableIterator(iter, ReadableOptions{Context: ctx, Route: route, Stdout: sink})
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, out.String(), "synthetic partial")
	require.Equal(t, 1, source.calls)
}
