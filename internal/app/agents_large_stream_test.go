package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsLargeTextStreamsPreserveProjectionAndMachineData(t *testing.T) {
	for _, reasoning := range []bool{false, true} {
		for _, count := range []int{257, 2000} {
			for _, format := range []string{"text", "jsonl", "raw"} {
				t.Run(fmt.Sprintf("reasoning=%t/parts=%d/%s", reasoning, count, format), func(t *testing.T) {
					var events, parts []string
					kind, index, partType, field := "output_text", "content_index", "output_text", "content"
					item := `"type":"message","role":"assistant"`
					if reasoning {
						kind, index, partType, field = "reasoning_summary_text", "summary_index", "summary_text", "summary"
						item = `"type":"reasoning"`
					}
					for i := range count {
						text := fmt.Sprintf("answer_%04d", i)
						for _, phase := range []string{"delta", "done"} {
							textField := "delta"
							if phase == "done" {
								textField = "text"
							}
							events = append(events, fmt.Sprintf(`{"type":"agent.session.turn.%s.%s","event_id":"evt_%d_%s","session_id":"sess_terminal","turn_id":"root","item_id":"msg_many","output_index":0,%q:%d,%q:%q}`, kind, phase, i, phase, index, i, textField, text))
						}
						parts = append(parts, fmt.Sprintf(`{"type":%q,"text":%q}`, partType, text))
					}
					// A final item and a late nonfinal update must not repeat completed text.
					events = append(events, fmt.Sprintf(`{"type":"agent.session.turn.item.done","event_id":"evt_snapshot","session_id":"sess_terminal","turn_id":"root","output_index":0,"item":{"id":"msg_many",%s,"turn_id":"root","status":"completed",%q:[%s]}}`, item, field, strings.Join(parts, ",")))
					events = append(events, fmt.Sprintf(`{"type":"agent.session.turn.%s.delta","event_id":"evt_late","session_id":"sess_terminal","turn_id":"root","item_id":"msg_many","output_index":0,%q:0,"delta":"LATE_SHOULD_NOT_REPEAT"}`, kind, index))
					events = append(events, agentsTerminalStatusEvent("completed", "root", `"completed"`, "null"))
					stream := &agentsOwnedStream{}
					for _, event := range events {
						stream.values = append(stream.values, gjson.Parse(event))
					}
					var out, diagnostic bytes.Buffer
					err := ShowJSONIterator(stream, -1, ShowJSONOpts{Context: t.Context(), Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent, Format: format, ExplicitFormat: true, Stdout: &out, Stderr: &diagnostic})
					require.NoError(t, err)
					require.Equal(t, 1, stream.closes)
					if format != "text" {
						require.Equal(t, strings.Join(events, "\n")+"\n", out.String())
						require.Empty(t, diagnostic.String())
						return
					}
					for i := range count {
						require.Equal(t, 1, strings.Count(out.String(), fmt.Sprintf("answer_%04d", i)), "part %d repeated or missing", i)
					}
					require.NotContains(t, out.String(), "LATE_SHOULD_NOT_REPEAT")
					require.NotContains(t, out.String(), "Content index:")
					require.NotContains(t, out.String(), "Summary index:")
				})
			}
		}
	}
}

func TestAgentsTextProjectionContinuesAcrossLongLivedTurns(t *testing.T) {
	stream := &agentsOwnedStream{}
	var events []string
	for turn := range 2 {
		for i := range 300 {
			events = append(events, fmt.Sprintf(`{"type":"agent.session.turn.output_text.done","event_id":"evt_%d_%d","session_id":"sess_terminal","turn_id":"turn_%d","item_id":"msg_%d","output_index":0,"content_index":0,"text":"turn_%d_answer_%03d"}`, turn, i, turn, i, turn, i))
		}
		events = append(events, agentsTerminalStatusEvent("completed", fmt.Sprintf("turn_%d", turn), `"completed"`, "null"))
	}
	for _, event := range events {
		stream.values = append(stream.values, gjson.Parse(event))
	}
	var out, diagnostic bytes.Buffer
	err := ShowJSONIterator(stream, -1, ShowJSONOpts{Context: t.Context(), Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent, Format: "text", ExplicitFormat: true, Stdout: &out, Stderr: &diagnostic})
	require.NoError(t, err)
	require.Equal(t, 1, stream.closes)
	require.Contains(t, out.String(), "Agent:\nturn_1_answer_299")
	require.NotContains(t, out.String(), "Content index:")
	for turn := range 2 {
		for i := range 300 {
			require.Equal(t, 1, strings.Count(out.String(), fmt.Sprintf("turn_%d_answer_%03d", turn, i)))
		}
	}
}
