package transformers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentsReasoningItem(kind, summary, status string) gjson.Result {
	return gjson.Parse(`{"type":"agent.session.turn.item.` + kind + `","event_id":"evt_item","session_id":"sess_test","turn_id":"turn_1","output_index":0,"item":{"id":"msg_1","type":"reasoning","turn_id":"turn_1","summary":` + summary + `,"status":` + status + `}}`)
}

func TestAgentsReasoningItemOnlySnapshots(t *testing.T) {
	for _, kind := range []string{"added", "done"} {
		for _, status := range []string{`null`, `"completed"`, `"incomplete"`, `"future"`, `123`} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				value := agentsReasoningItem(kind, `[{"type":"summary_text","text":"History summary"}]`, status)
				if kind == "added" {
					value = gjson.Parse(strings.Replace(value.Raw, `"output_index":0`, `"output_index":null`, 1))
				}
				var projector AgentsStreamProjector
				event, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
				require.NoError(t, err)
				require.True(t, projected)
				require.Len(t, event.Parts, 1)
				require.Equal(t, "History summary", event.Parts[0].Text)
				require.Equal(t, "Reasoning", event.Parts[0].Label)
				require.True(t, event.Parts[0].Snapshot)
				require.Equal(t, status, event.Details.Get("item.status").Raw)
				require.False(t, projector.HasOmissions())
			})
		}
	}
}

func TestAgentsReasoningItemMixedSnapshots(t *testing.T) {
	output := agentsRender(t,
		agentsReasoningItem("added", `[]`, `null`),
		agentsReasoningEvent("text.delta", `"delta":"First "`),
		agentsReasoningItem("added", `[{"type":"summary_text","text":"First summary"}]`, `"in_progress"`),
		agentsReasoningEvent("text.done", `"text":"First summary"`),
		agentsReasoningItem("done", `[{"type":"summary_text","text":"First summary"},{"type":"summary_text","text":"Second summary"}]`, `"incomplete"`),
		agentsReasoningEvent("part.done", `"part":{"type":"summary_text","text":"First summary"},"status":"incomplete"`),
		agentsReasoningItem("added", `[{"type":"summary_text","text":"STALE"}]`, `null`),
		agentsTextEvent("delta", "answer", "turn_1", "msg_1", "Answer"))
	// Item status separates output blocks, but the original delta remains singular.
	require.Equal(t, 1, strings.Count(output, "First "))
	require.Equal(t, 1, strings.Count(output, "Second summary"))
	require.NotContains(t, output, "STALE")
	require.Contains(t, output, "incomplete")
	require.Contains(t, output, "Agent:\nAnswer")
}

func TestAgentsReasoningItemUnknownData(t *testing.T) {
	value := agentsReasoningItem("done", `[{"type":"summary_text","text":"Summary","annotations":null,"annotations":[]},{"type":"future_part","opaque":"not-base64!"}]`, `null`)
	value = gjson.Parse(strings.Replace(value.Raw, `"status":null`, `"status":null,"encrypted_content":"not-base64!\\opaque","future":1,"future":2`, 1))
	event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected)
	require.Len(t, event.Parts, 1)
	require.Contains(t, event.Details.Raw, `"annotations":null,"annotations":[]`)
	require.Contains(t, event.Details.Raw, `{"type":"future_part","opaque":"not-base64!"}`)
	require.Contains(t, event.Details.Raw, `"encrypted_content":"not-base64!\\opaque","future":1,"future":2`)
}

func TestAgentsReasoningItemFallbackPreservesState(t *testing.T) {
	base := agentsReasoningItem("done", `[{"type":"summary_text","text":"First"},{"type":"summary_text","text":"Second"}]`, `null`).Raw
	for name, change := range map[string][2]string{
		"envelope_type":        {`"type":"agent.session.turn.item.done"`, `"type":"agent.session.turn.item.done","type":"future"`},
		"event_id":             {`"event_id":"evt_item"`, `"event_id":"evt_item","event_id":"other"`},
		"session_id":           {`"session_id":"sess_test"`, `"session_id":"sess_test","session_id":"other"`},
		"outer_turn":           {`"turn_id":"turn_1","output_index"`, `"turn_id":"turn_1","turn_id":"other","output_index"`},
		"output_index":         {`"output_index":0`, `"output_index":0,"output_index":1`},
		"item":                 {`"item":`, `"item":{"type":"reasoning","summary":[]},"item":`},
		"item_id":              {`"id":"msg_1"`, `"id":"msg_1","id":"other"`},
		"item_type":            {`"type":"reasoning"`, `"type":"reasoning","type":"future"`},
		"item_turn":            {`"turn_id":"turn_1","summary"`, `"turn_id":"other","summary"`},
		"null_item_turn":       {`"turn_id":"turn_1","summary"`, `"turn_id":null,"summary"`},
		"null_outer_turn":      {`"turn_id":"turn_1","output_index"`, `"turn_id":null,"output_index"`},
		"summary":              {`"summary":`, `"summary":[],"summary":`},
		"escaped_summary":      {`"summary":`, `"summ\u0061ry":[],"summary":`},
		"case_summary":         {`"summary":`, `"SUMMARY":[],"summary":`},
		"status":               {`"status":null`, `"status":null,"status":"incomplete"`},
		"part_type":            {`"type":"summary_text"`, `"type":"summary_text","type":"future"`},
		"part_text":            {`"text":"First"`, `"text":"First","text":"other"`},
		"malformed_later_text": {`"text":"Second"`, `"text":123`},
		"missing_status":       {`,"status":null`, ``},
		"nonarray_summary":     {`"summary":[{"type":"summary_text","text":"First"},{"type":"summary_text","text":"Second"}]`, `"summary":null`},
	} {
		t.Run(name, func(t *testing.T) {
			var projector AgentsStreamProjector
			_, _, err := projector.Project(t.Context(), agentsReasoningEvent("text.delta", `"delta":"Cached"`), agentsEventsRoute)
			require.NoError(t, err)
			before := projector
			value := gjson.Parse(strings.Replace(base, change[0], change[1], 1))
			event, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			require.Empty(t, event.Parts)
			if projected {
				require.Equal(t, value.Raw, event.Details.Raw)
			}
			require.Equal(t, before, projector)
			event, projected, err = projector.Project(t.Context(), agentsReasoningEvent("text.delta", `"delta":"Valid later delta"`), agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, projected)
			require.Equal(t, "Valid later delta", event.Parts[0].Text)
		})
	}
}

func TestAgentsReasoningItemCapacityRollback(t *testing.T) {
	var projector AgentsStreamProjector
	for i := range agentsTrackedParts - 1 {
		value := agentsReasoningEvent("text.delta", `"delta":"Cached"`)
		value = gjson.Parse(strings.Replace(value.Raw, `"item_id":"msg_1"`, fmt.Sprintf(`"item_id":"other_%d"`, i), 1))
		_, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
		require.NoError(t, err)
		require.True(t, projected)
	}
	before := projector
	value := agentsReasoningItem("done", `[{"type":"summary_text","text":"First"},{"type":"summary_text","text":"Second"}]`, `null`)
	_, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
	require.NoError(t, err)
	require.False(t, projected)
	require.Equal(t, before, projector)
	_, projected, err = projector.Project(t.Context(), agentsReasoningItem("done", `[{"type":"summary_text","text":"Fits"}]`, `null`), agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected)
}

func TestAgentsReasoningItemCancellationRollback(t *testing.T) {
	value := agentsReasoningItem("done", `[{"type":"summary_text","text":"First"},{"type":"summary_text","text":"Second"}]`, `null`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	completed := &cancelSummaryContext{Context: ctx, cancel: cancel, after: -1}
	_, projected, err := new(AgentsStreamProjector).Project(completed, value, agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected)
	// Include the final context check after all parts have modified staged state.
	for after := int32(1); after <= completed.checks.Load(); after++ {
		var projector AgentsStreamProjector
		ctx, cancel := context.WithCancel(t.Context())
		controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
		_, _, err := projector.Project(controlled, value, agentsEventsRoute)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, AgentsStreamProjector{}, projector)
		cancel()
	}
}
