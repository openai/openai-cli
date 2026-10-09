package custom

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentsFunctionResultImagesPreserveHumanAndMachineData(t *testing.T) {
	encoded := strings.Repeat("QUJD", 32768)
	url := "data:image/png;base64," + encoded
	value := `{"type":"agent.session.turn.item.added","event_id":"evt_function_image","session_id":"sess_test","turn_id":"turn_test","output_index":null,"item":{"id":"fco_test","turn_id":"turn_test","type":"function_call_output","call_id":"call_test","status":"completed","output":[{"type":"input_text","text":"Before image"},{"type":"input_image","image_url":` + strconv.Quote(url) + `},{"type":"input_text","text":"After image"}],"error":null,"future_item":9007199254740993}}`
	for _, format := range []string{"auto", "text", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			err := ShowJSONIterator(streamItems(value), 1, ShowJSONOpts{
				Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
				Format: format, Stdout: &out, Stderr: &diagnostic,
			})
			require.NoError(t, err)
			if format == "raw" || format == "jsonl" {
				require.Equal(t, value+"\n", out.String())
				require.Empty(t, diagnostic.String())
				return
			}
			if strings.Contains(out.String(), encoded) {
				t.Fatalf("human tool result retained %d encoded characters", len(encoded))
			}
			for _, field := range []string{"function_call_output", "call_test", "completed", "9007199254740993", "image/png; 131072 base64 characters"} {
				require.Contains(t, out.String(), field)
			}
			require.Less(t, strings.Index(out.String(), "Before image"), strings.Index(out.String(), "image/png;"))
			require.Less(t, strings.Index(out.String(), "image/png;"), strings.Index(out.String(), "After image"))
			require.Equal(t, "Some fields omitted. Use --format jsonl for complete events.\n", diagnostic.String())
		})
	}
	var out, diagnostic bytes.Buffer
	require.NoError(t, ShowJSONIterator(streamItems(value), 1, ShowJSONOpts{
		Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
		Format: "text", Transform: "item.output.1.image_url", RawOutput: true, Stdout: &out, Stderr: &diagnostic,
	}))
	require.Equal(t, url+"\n", out.String())
	require.Empty(t, diagnostic.String())
}
