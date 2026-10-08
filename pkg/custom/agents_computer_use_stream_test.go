package custom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentsComputerUseStreamHumanAndMachineOutput(t *testing.T) {
	encoded := strings.Repeat("QUJD", 32768)
	imageURL := "data:image/jpeg;base64," + encoded
	value := `{"type":"agent.session.turn.item.done","event_id":"ev_computer","session_id":"sess_test","turn_id":"turn_test","output_index":0,"item":{"id":"computer_test","type":"computer_use_call","turn_id":"turn_test","title":"Read the synthetic report","status":"completed","output":{"type":"computer_screenshot","image_url":` + strconv.Quote(imageURL) + `}},"future_event":9007199254740993}`
	for _, operation := range []string{
		"(resource) beta.agents.sessions > (method) create",
		"(resource) beta.agents.sessions.events > (method) stream",
	} {
		for _, format := range []string{"text", "auto", "json", "jsonl", "raw"} {
			t.Run(operation+"/"+format, func(t *testing.T) {
				var out, diagnostic bytes.Buffer
				iter := streamItems(value)
				err := ShowJSONIterator(iter, 1, ShowJSONOpts{
					Operation: operation, OutputKind: OutputStreamEvent,
					Format: format, ExplicitFormat: format != "auto", Stdout: &out, Stderr: &diagnostic,
				})
				require.NoError(t, err)
				require.Equal(t, 1, iter.calls)
				if format == "text" || format == "auto" {
					if strings.Contains(out.String(), encoded) {
						t.Fatalf("human output retained %d encoded image characters", len(encoded))
					}
					for _, retained := range []string{"computer_test", "computer_use_call", "completed", "Read the synthetic report", "computer_screenshot", "9007199254740993"} {
						require.Contains(t, out.String(), retained)
					}
					require.Contains(t, out.String(), fmt.Sprintf("(JPEG screenshot; %d base64 characters)", len(encoded)))
					require.Equal(t, "Some fields omitted. Use --format jsonl for complete events.\n", diagnostic.String())
				} else {
					require.Empty(t, diagnostic.String())
					if format == "raw" {
						require.Equal(t, value+"\n", out.String())
					}
					var original, actual any
					for _, target := range []struct {
						source string
						value  *any
					}{{value, &original}, {out.String(), &actual}} {
						decoder := json.NewDecoder(strings.NewReader(target.source))
						decoder.UseNumber()
						require.NoError(t, decoder.Decode(target.value))
						require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
					}
					require.Equal(t, original, actual, "machine events must preserve the complete screenshot and unfamiliar fields")
				}
			})
		}
		var out, diagnostic bytes.Buffer
		require.NoError(t, ShowJSONIterator(streamItems(value), 1, ShowJSONOpts{
			Operation: operation, OutputKind: OutputStreamEvent, Format: "text", ExplicitFormat: true,
			Transform: "item.output.image_url", RawOutput: true, Stdout: &out, Stderr: &diagnostic,
		}))
		require.Equal(t, imageURL+"\n", out.String())
		require.Empty(t, diagnostic.String())
	}
}
