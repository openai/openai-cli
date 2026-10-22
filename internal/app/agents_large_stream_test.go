package app

import (
	"bytes"
	"fmt"
	"io"
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

// Identity fields can follow a large array. Their position must not change
// projection, and resolving them must not rescan the array for every part.
func agentsLargeSnapshotFixture(reasoning, lateIdentity bool, count, idBytes int) ([]string, []string) {
	sessionID := "sess_" + strings.Repeat("s", idBytes)
	turnID := "turn_" + strings.Repeat("t", idBytes)
	itemID := "item_" + strings.Repeat("i", idBytes)
	field, partType, deltaKind, index := "content", "output_text", "output_text", "content_index"
	metadata := `"type":"message","role":"assistant","status":"completed"`
	if reasoning {
		field, partType, deltaKind, index = "summary", "summary_text", "reasoning_summary_text", "summary_index"
		metadata = `"type":"reasoning","status":"completed"`
	}
	texts := make([]string, count)
	var parts strings.Builder
	for i := range count {
		texts[i] = fmt.Sprintf("snapshot_%05d:%s", i, strings.Repeat("x", 64))
		if i > 0 {
			parts.WriteByte(',')
		}
		fmt.Fprintf(&parts, `{"type":%q,"text":%q}`, partType, texts[i])
	}
	itemIdentity := fmt.Sprintf(`"id":%q,"turn_id":%q`, itemID, turnID)
	array := fmt.Sprintf(`%q:[%s]`, field, parts.String())
	item := "{" + itemIdentity + "," + metadata + "," + array + "}"
	if lateIdentity {
		item = "{" + array + "," + metadata + "," + itemIdentity + "}"
	}
	envelopeIdentity := fmt.Sprintf(`"session_id":%q,"turn_id":%q,"output_index":0`, sessionID, turnID)
	snapshot := func(eventID string) string {
		prefix := fmt.Sprintf(`{"type":"agent.session.turn.item.done","event_id":%q,`, eventID)
		if lateIdentity {
			return prefix + `"item":` + item + "," + envelopeIdentity + "}"
		}
		return prefix + envelopeIdentity + `,"item":` + item + "}"
	}
	// A changed event ID avoids relying on duplicate-event suppression.
	events := []string{snapshot("evt_snapshot"), snapshot("evt_duplicate_snapshot")}
	for _, position := range []int{0, count / 2, count - 1} {
		events = append(events, fmt.Sprintf(`{"type":"agent.session.turn.%s.delta","event_id":"evt_late_%d",%s,"item_id":%q,%q:%d,"delta":"LATE_SHOULD_NOT_REPEAT"}`, deltaKind, position, envelopeIdentity, itemID, index, position))
	}
	events = append(events, fmt.Sprintf(`{"type":"agent.session.turn.completed","session_id":%q,"turn_id":%q,"turn":{"id":%q,"session_id":%q,"subagent_id":null,"status":"completed"}}`, sessionID, turnID, turnID, sessionID))
	return events, texts
}

func TestAgentsLargeLateIdentitySnapshotsPreserveProjectionAndMachineData(t *testing.T) {
	for _, reasoning := range []bool{false, true} {
		for _, format := range []string{"text", "jsonl", "raw"} {
			t.Run(fmt.Sprintf("reasoning=%t/%s", reasoning, format), func(t *testing.T) {
				events, texts := agentsLargeSnapshotFixture(reasoning, true, 4096, 1024)
				stream := &agentsOwnedStream{}
				for _, event := range events {
					stream.values = append(stream.values, gjson.Parse(event))
				}
				var out, diagnostic bytes.Buffer
				err := ShowJSONIterator(stream, -1, ShowJSONOpts{
					Context: t.Context(), Operation: "(resource) beta.agents.sessions.events > (method) stream",
					OutputKind: OutputStreamEvent, Format: format, ExplicitFormat: true,
					Stdout: &out, Stderr: &diagnostic,
				})
				require.NoError(t, err)
				require.Equal(t, len(events)+1, stream.reads)
				require.Equal(t, 1, stream.closes)
				if format != "text" {
					require.Equal(t, strings.Join(events, "\n")+"\n", out.String())
					require.Empty(t, diagnostic.String())
					return
				}
				// Scan rendered text once so the assertion itself stays linear.
				counts := make(map[string]int, len(texts))
				for line := range strings.SplitSeq(out.String(), "\n") {
					if strings.HasPrefix(line, "snapshot_") {
						counts[line]++
					}
				}
				require.Len(t, counts, len(texts))
				for _, text := range texts {
					require.Equal(t, 1, counts[text], "snapshot text repeated or missing: %s", text)
				}
				label := "Agent:\n"
				if reasoning {
					label = "Reasoning:\n"
				}
				require.Contains(t, out.String(), label+texts[0])
				require.NotContains(t, out.String(), "LATE_SHOULD_NOT_REPEAT")
				require.NotContains(t, out.String(), "Content index:")
				require.NotContains(t, out.String(), "Summary index:")
			})
		}
	}
}

func BenchmarkAgentsSnapshotIdentityOrder(b *testing.B) {
	for _, reasoning := range []bool{false, true} {
		for _, count := range []int{512, 4096} {
			for _, lateIdentity := range []bool{false, true} {
				b.Run(fmt.Sprintf("reasoning=%t/parts=%d/late_identity=%t", reasoning, count, lateIdentity), func(b *testing.B) {
					events, _ := agentsLargeSnapshotFixture(reasoning, lateIdentity, count, 1024)
					values := make([]gjson.Result, len(events))
					var size int64
					for i, event := range events {
						values[i] = gjson.Parse(event)
						size += int64(len(event))
					}
					b.SetBytes(size)
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						stream := &agentsOwnedStream{values: values}
						err := ShowJSONIterator(stream, -1, ShowJSONOpts{
							Context: b.Context(), Operation: "(resource) beta.agents.sessions.events > (method) stream",
							OutputKind: OutputStreamEvent, Format: "text", ExplicitFormat: true,
							Stdout: io.Discard, Stderr: io.Discard,
						})
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
