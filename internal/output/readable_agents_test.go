package output

import (
	"bytes"
	"errors"
	"syscall"
	"testing"

	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsReadableStreamDiagnosticPolicyAndTrailingRecords(t *testing.T) {
	const hint = "Some fields omitted. Use --format jsonl for complete events.\n"
	route := transformers.Route{Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: transformers.OutputStreamEvent}
	upstream := errors.New("synthetic stream failure")
	for _, allowed := range []bool{false, true} {
		for _, fail := range []error{nil, syscall.EPIPE} {
			var out, diagnostic bytes.Buffer
			source := &transformTestIterator{items: []any{
				JSON{gjson.Parse(`{"type":"agent.output.command_execution_output.delta","delta":"synthetic tool transcript"}`)},
				JSON{gjson.Parse(`{"type":"agent.output.command_execution_output.delta","delta":"synthetic later transcript"}`)},
				JSON{gjson.Parse(`{"type":"future.event","marker":"trailing data"}`)},
			}, err: upstream}
			iter := NewIterator(source, IteratorOptions{Context: t.Context(), Transform: transformers.Identity, Route: route, Limit: -1})
			stderr := writeFunc(func(data []byte) (int, error) {
				require.Equal(t, 4, source.calls, "hint follows the complete stream")
				if fail != nil {
					return 0, fail
				}
				return diagnostic.Write(data)
			})
			err := WriteReadableIterator(iter, ReadableOptions{Context: t.Context(), Route: route,
				FormatOptions: FormatOptions{Format: "text"}, Stdout: &out, Stderr: stderr, DiagnosticsAllowed: allowed})
			require.ErrorIs(t, err, upstream)
			require.NotContains(t, out.String(), "transcript")
			require.Contains(t, out.String(), "trailing data")
			require.NotContains(t, out.String(), hint)
			if allowed && fail != nil {
				require.ErrorIs(t, err, fail)
				var diagnosticError *DiagnosticError
				require.ErrorAs(t, err, &diagnosticError)
				require.False(t, IsBrokenPipe(err))
			} else if allowed {
				require.Equal(t, hint, diagnostic.String())
			} else {
				require.Empty(t, diagnostic.String())
				require.False(t, errors.Is(err, syscall.EPIPE))
			}
		}
	}
}
