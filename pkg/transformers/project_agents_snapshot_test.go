package transformers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentsAssistantSnapshot(status, content string) gjson.Result {
	return gjson.Parse(`{"type":"agent.session.turn.item.done","event_id":"evt_snapshot","session_id":"sess_test","turn_id":"turn_1","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":` + status + `,"turn_id":"turn_1","phase":"final_answer","content":` + content + `}}`)
}

func TestAgentsMessageSnapshotRetainsNonRoutineStatus(t *testing.T) {
	for _, status := range []string{`"incomplete"`, `"queued"`, `"future_status"`, `null`, `42`, `false`, `{}`, `[]`, `""`} {
		t.Run(status, func(t *testing.T) {
			value := agentsAssistantSnapshot(status, `[{"type":"output_text","text":"Partial answer"}]`)
			event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, projected)
			require.Equal(t, status, event.Details.Get("item.status").Raw)
		})
	}
	for _, status := range []string{`"completed"`, `"in_progress"`} {
		value := agentsAssistantSnapshot(status, `[{"type":"output_text","text":"Routine answer"}]`)
		event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
		require.NoError(t, err)
		require.True(t, projected)
		require.False(t, event.Details.Get("item.status").Exists())
	}
}

func TestAgentsMessageSnapshotDuplicateContentKeepsLaterData(t *testing.T) {
	value := agentsAssistantSnapshot(`"completed"`, `[{"type":"output_text","text":"First answer"}]`)
	raw := strings.TrimSuffix(value.Raw, "}}") + `,"content":[{"type":"output_text","text":"Later answer"},{"type":"future_part","future":9007199254740993}]}}`
	output := agentsRender(t, gjson.Parse(raw))
	require.Contains(t, output, "First answer")
	require.Contains(t, output, "Later answer")
	require.Contains(t, output, "9007199254740993")
}

func TestAgentsAssistantAddedAcceptsNullOutputIndex(t *testing.T) {
	value := agentsAssistantSnapshot(`"completed"`, `[{"type":"output_text","text":"Historical assistant answer"}]`)
	raw := strings.Replace(value.Raw, `"output_index":0`, `"output_index":null`, 1)
	raw = strings.Replace(raw, "agent.session.turn.item.done", "agent.session.turn.item.added", 1)
	var projector AgentsStreamProjector
	event, projected, err := projector.Project(t.Context(), gjson.Parse(raw), agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected, "item.added explicitly permits a null output index")
	require.Len(t, event.Parts, 1)
	require.Equal(t, "Historical assistant answer", event.Parts[0].Text)
	require.False(t, projector.parts[0].final)
}

func TestAgentsMessageSnapshotFallbackDoesNotFinalizeCachedParts(t *testing.T) {
	for _, scenario := range []string{"invalid_later_part", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			var projector AgentsStreamProjector
			_, ok, err := projector.Project(t.Context(), agentsTextEvent("delta", "evt_cached", "turn_1", "msg_1", "Existing "), agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, ok)
			content := `[{"type":"output_text","text":"Existing answer"},{"type":"output_text","text":42}]`
			if scenario == "capacity" {
				for i := 0; i < agentsTrackedParts-2; i++ {
					_, _, err = projector.Project(t.Context(), agentsTextEvent("done", fmt.Sprint(i), "other_turn", fmt.Sprint(i), "Other answer"), agentsEventsRoute)
					require.NoError(t, err)
				}
				content = `[{"type":"output_text","text":"Existing answer"},{"type":"output_text","text":"New second part"},{"type":"output_text","text":"New third part"}]`
			}
			before := projector
			_, projected, err := projector.Project(context.Background(), agentsAssistantSnapshot(`"completed"`, content), agentsEventsRoute)
			require.NoError(t, err)
			require.False(t, projected)
			if projector.parts != before.parts || projector.partCount != before.partCount || projector.recent != before.recent {
				t.Error("fallback mutated the existing bounded cache")
			}
			event, projected, err := projector.Project(t.Context(), agentsTextEvent("delta", "evt_recovery", "turn_1", "msg_1", "Recovered delta"), agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, projected)
			require.Len(t, event.Parts, 1, "fallback must not suppress later valid data")
			require.Equal(t, "Recovered delta", event.Parts[0].Text)
		})
	}
}

func TestAgentsAssistantAnnotationsRemainUnfamiliarData(t *testing.T) {
	for _, annotations := range []string{`null`, `[]`, `{}`, `[{"type":"artifact","artifact_id":"artifact_synthetic"}]`} {
		for _, repeated := range []bool{false, true} {
			part := `{"type":"output_text","text":"Answer","annotations":` + annotations
			if repeated {
				part += `,"annotations":{"future":9007199254740993}`
			}
			part += `}`
			values := map[string]gjson.Result{
				"item": agentsAssistantSnapshot(`"completed"`, "["+part+"]"),
				"part": gjson.Parse(`{"type":"agent.session.turn.content_part.done","event_id":"evt_part","session_id":"sess_test","turn_id":"turn_1","item_id":"msg_1","output_index":0,"content_index":0,"part":` + part + `}`),
			}
			for name, value := range values {
				t.Run(fmt.Sprintf("%s/%s/repeated=%t", name, annotations, repeated), func(t *testing.T) {
					event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
					require.NoError(t, err)
					require.True(t, projected)
					count := 1
					if repeated {
						count++
						require.Contains(t, event.Details.Raw, `"annotations":{"future":9007199254740993}`)
					}
					require.Contains(t, event.Details.Raw, `"annotations":`+annotations)
					require.Equal(t, count, strings.Count(event.Details.Raw, `"annotations"`))
				})
			}
		}
	}
}

func TestAgentsAssistantAmbiguityPreservesExistingCache(t *testing.T) {
	message := agentsAssistantSnapshot(`"completed"`, `[{"type":"output_text","text":"FIRST","annotations":[]}]`).Raw
	textDone := agentsTextEvent("done", "evt_done", "turn_1", "msg_1", "FIRST").Raw
	textDelta := agentsTextEvent("delta", "evt_delta", "turn_1", "msg_1", "FIRST").Raw
	partDone := `{"type":"agent.session.turn.content_part.done","event_id":"evt_part","session_id":"sess_test","turn_id":"turn_1","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"FIRST"}}`
	values := map[string]string{
		"content":                strings.Replace(message, `"content":`, `"content":[],"content":`, 1),
		"escaped_content":        strings.Replace(message, `"content":`, `"cont\u0065nt":[],"content":`, 1),
		"case_content":           strings.Replace(message, `"content":`, `"Content":[],"content":`, 1),
		"status":                 strings.Replace(message, `"status":"completed"`, `"status":"completed","status":"incomplete"`, 1),
		"item_id":                strings.Replace(message, `"id":"msg_1"`, `"id":"msg_1","id":"msg_2"`, 1),
		"item_type":              strings.Replace(message, `"type":"message"`, `"type":"message","type":"message"`, 1),
		"item_role":              strings.Replace(message, `"role":"assistant"`, `"role":"assistant","role":"assistant"`, 1),
		"item_turn_conflict":     strings.Replace(message, `"phase":"final_answer"`, `"phase":"final_answer","turn_id":"turn_other"`, 1),
		"different_item_turn":    strings.Replace(message, `"turn_id":"turn_1","phase"`, `"turn_id":"turn_other","phase"`, 1),
		"envelope_type":          strings.Replace(message, `"event_id":`, `"type":"agent.session.turn.item.done","event_id":`, 1),
		"envelope_event_id":      strings.Replace(message, `"event_id":"evt_snapshot"`, `"event_id":"evt_snapshot","event_id":"evt_other"`, 1),
		"envelope_session_id":    strings.Replace(message, `"session_id":"sess_test"`, `"session_id":"sess_test","session_id":"sess_other"`, 1),
		"envelope_turn_id":       strings.Replace(message, `"turn_id":"turn_1"`, `"turn_id":"turn_1","turn_id":"turn_other"`, 1),
		"envelope_output_index":  strings.Replace(message, `"output_index":0`, `"output_index":0,"output_index":1`, 1),
		"malformed_event_id":     strings.Replace(message, `"event_id":"evt_snapshot"`, `"event_id":null`, 1),
		"malformed_output_index": strings.Replace(message, `"output_index":0`, `"output_index":{}`, 1),
		"part_type":              strings.Replace(message, `"type":"output_text"`, `"type":"output_text","type":"future_part"`, 1),
		"part_text":              strings.Replace(message, `"text":"FIRST"`, `"text":"FIRST","text":"SECOND"`, 1),
		"part_escaped_text":      strings.Replace(message, `"text":"FIRST"`, `"text":"FIRST","te\u0078t":"SECOND"`, 1),
		"text_done":              strings.TrimSuffix(textDone, "}") + `,"text":"SECOND"}`,
		"text_delta":             strings.TrimSuffix(textDelta, "}") + `,"delta":"SECOND"}`,
		"text_identity":          strings.TrimSuffix(textDone, "}") + `,"item_id":"msg_2"}`,
		"text_content_index":     strings.TrimSuffix(textDone, "}") + `,"content_index":1}`,
		"text_missing_index":     strings.Replace(textDone, `"content_index":0,`, "", 1),
		"part_missing_index":     strings.Replace(partDone, `"content_index":0,`, "", 1),
		"content_part_text":      strings.Replace(partDone, `"text":"FIRST"`, `"text":"FIRST","text":"SECOND"`, 1),
		"content_part_type":      strings.Replace(partDone, `"type":"output_text"`, `"type":"output_text","type":"output_text"`, 1),
		"content_part_container": strings.TrimSuffix(partDone, "}") + `,"part":{"type":"output_text","text":"SECOND"}}`,
	}
	for name, raw := range values {
		for _, cached := range []string{"delta", "done"} {
			t.Run(name+"/cached_"+cached, func(t *testing.T) {
				require.True(t, gjson.Valid(raw))
				var projector AgentsStreamProjector
				_, _, err := projector.Project(t.Context(), agentsTextEvent(cached, "evt_cached", "turn_1", "msg_1", "Existing"), agentsEventsRoute)
				require.NoError(t, err)
				before := projector
				event, projected, err := projector.Project(t.Context(), gjson.Parse(raw), agentsEventsRoute)
				require.NoError(t, err)
				if projected {
					require.Empty(t, event.Parts)
					require.Equal(t, raw, event.Details.Raw, "fallback may retain the image pass's full record")
				}
				if projector != before {
					t.Fatal("ambiguous event changed bounded part or recent-event state")
				}
			})
		}
	}
}

func TestAgentsAssistantSnapshotCancellationDoesNotCommitState(t *testing.T) {
	value := agentsAssistantSnapshot(`"completed"`, `[{"type":"output_text","text":"FIRST"},{"type":"output_text","text":"SECOND"}]`)
	for after := int32(1); after <= 55; after++ {
		ctx, cancel := context.WithCancel(t.Context())
		controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
		var projector AgentsStreamProjector
		_, _, err := projector.Project(t.Context(), agentsTextEvent("delta", "evt_cached", "turn_1", "msg_1", "Existing"), agentsEventsRoute)
		require.NoError(t, err)
		before := projector
		_, _, err = projector.Project(controlled, value, agentsEventsRoute)
		if err != nil {
			require.ErrorIs(t, err, context.Canceled)
			if projector != before {
				t.Fatalf("cancelled snapshot committed state at context check %d", after)
			}
		}
		cancel()
	}
}
