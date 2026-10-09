package custom

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentsUserImagesHumanAndMachineStreams(t *testing.T) {
	encoded := strings.Repeat("QUJD", 32768)
	url := "data:image/png;base64," + encoded
	value := `{"type":"agent.session.turn.item.done","event_id":"ev_user","session_id":"sess_test","turn_id":"turn_test","output_index":0,"future_event":9007199254740993,"item":{"id":null,"type":"message","turn_id":"turn_test","role":"user","status":"completed","phase":null,"content":[{"type":"input_text","text":"First user text"},{"type":"input_image","image_url":` + strconv.Quote(url) + `},{"type":"input_text","text":"Last user text"}]}}`
	for _, operation := range []string{
		"(resource) beta.agents.sessions > (method) create",
		"(resource) beta.agents.sessions.events > (method) stream",
	} {
		for _, format := range []string{"auto", "text", "json", "jsonl", "raw"} {
			t.Run(operation+"/"+format, func(t *testing.T) {
				var out, diagnostic bytes.Buffer
				iter := streamItems(value)
				require.NoError(t, ShowJSONIterator(iter, 1, ShowJSONOpts{
					Operation: operation, OutputKind: OutputStreamEvent, Format: format,
					ExplicitFormat: format != "auto", Stdout: &out, Stderr: &diagnostic,
				}))
				require.Equal(t, 1, iter.calls)
				if format == "auto" || format == "text" {
					if strings.Contains(out.String(), encoded) {
						t.Fatalf("human output retained %d encoded image characters", len(encoded))
					}
					require.Contains(t, out.String(), "(image/png; 131072 base64 characters)")
					first, image, last := strings.Index(out.String(), "First user text"), strings.Index(out.String(), "(image/png;"), strings.Index(out.String(), "Last user text")
					require.True(t, first >= 0 && first < image && image < last, "mixed content must keep its source order")
					require.Contains(t, out.String(), "9007199254740993")
					require.Equal(t, "Some fields omitted. Use --format jsonl for complete events.\n", diagnostic.String())
				} else {
					require.Empty(t, diagnostic.String())
					if format == "raw" {
						require.Equal(t, value+"\n", out.String())
					}
					var original, actual any
					for _, target := range []struct {
						text  string
						value *any
					}{{value, &original}, {out.String(), &actual}} {
						decoder := json.NewDecoder(strings.NewReader(target.text))
						decoder.UseNumber()
						require.NoError(t, decoder.Decode(target.value))
						require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
					}
					require.Equal(t, original, actual)
				}
			})
		}
		var out, diagnostic bytes.Buffer
		require.NoError(t, ShowJSONIterator(streamItems(value), 1, ShowJSONOpts{
			Operation: operation, OutputKind: OutputStreamEvent, Format: "text", ExplicitFormat: true,
			Transform: "item.content.1.image_url", RawOutput: true, Stdout: &out, Stderr: &diagnostic,
		}))
		require.Equal(t, url+"\n", out.String())
		require.Empty(t, diagnostic.String())
	}
}
