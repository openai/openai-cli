package transformers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentsReasoningEvent(kind, fields string) gjson.Result {
	return gjson.Parse(`{"type":"agent.session.turn.reasoning_summary_` + kind + `","event_id":"evt_reasoning","session_id":"sess_test","turn_id":"turn_1","item_id":"msg_1","output_index":0,"summary_index":0,` + fields + `}`)
}

func TestAgentsReasoningIncrementalSnapshots(t *testing.T) {
	var projector AgentsStreamProjector
	var output bytes.Buffer
	writer := readable.NewStreamWriter(&output)
	for i, value := range []gjson.Result{
		agentsReasoningEvent("part.added", `"part":{"type":"summary_text","text":""}`),
		agentsReasoningEvent("text.delta", `"delta":"Consider "`),
		agentsReasoningEvent("text.delta", `"delta":"both choices"`),
		agentsReasoningEvent("text.done", `"text":"Consider both choices"`),
		agentsReasoningEvent("part.done", `"part":{"type":"summary_text","text":"Consider both choices"},"status":null`),
	} {
		original := value.Raw
		event, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
		require.NoError(t, err)
		require.True(t, projected)
		require.NoError(t, writer.Write(event))
		require.Equal(t, original, value.Raw)
		if i == 1 {
			require.Equal(t, "Reasoning:\nConsider ", output.String(), "emit before the next event")
		}
	}
	require.NoError(t, writer.Finish())
	require.Equal(t, 1, strings.Count(output.String(), "Consider both choices"))
	require.Contains(t, output.String(), "Status: (null)")
}

func TestAgentsReasoningIdentityAndSnapshots(t *testing.T) {
	t.Run("maximum_schema_indices", func(t *testing.T) {
		value := agentsReasoningEvent("text.done", `"text":"Maximum indices"`)
		value = gjson.Parse(strings.NewReplacer(
			`"output_index":0`, `"output_index":4294967295`,
			`"summary_index":0`, `"summary_index":4294967295`,
		).Replace(value.Raw))
		require.Equal(t, "Reasoning:\nMaximum indices\n", agentsRender(t, value))
	})
	t.Run("separate_agent_and_summary_indices", func(t *testing.T) {
		first := agentsReasoningEvent("text.done", `"text":"First reasoning"`)
		second := gjson.Parse(strings.Replace(first.Raw, `"summary_index":0`, `"summary_index":1`, 1))
		second = gjson.Parse(strings.Replace(second.Raw, "First reasoning", "Second reasoning", 1))
		output := agentsRender(t, first, second, agentsTextEvent("delta", "agent", "turn_1", "msg_1", "Answer"))
		require.Contains(t, output, "Reasoning:\nFirst reasoning")
		require.Contains(t, output, "Reasoning:\nSecond reasoning")
		require.Contains(t, output, "Agent:\nAnswer")
	})
	t.Run("repeat_changed_id_and_late_delta", func(t *testing.T) {
		first := agentsReasoningEvent("text.delta", `"delta":"First "`)
		output := agentsRender(t, first, first,
			agentsReasoningEvent("text.delta", `"delta":"choice"`),
			agentsReasoningEvent("text.done", `"text":"First choice"`),
			agentsReasoningEvent("text.delta", `"delta":"LATE"`),
			agentsReasoningEvent("text.done", `"text":"Revised choice"`))
		require.Equal(t, 1, strings.Count(output, "First choice"))
		require.NotContains(t, output, "LATE")
		require.Contains(t, output, "Updated Reasoning:\nRevised choice")
	})
}

func TestAgentsReasoningPreservesMetadata(t *testing.T) {
	for _, status := range []string{`null`, `"incomplete"`, `"future"`, `123`, `{}`, `[]`} {
		t.Run(status, func(t *testing.T) {
			value := agentsReasoningEvent("part.done", `"part":{"type":"summary_text","text":"Summary","annotations":null,"annotations":[],"future":1,"future":2},"status":`+status+`,"extension":true`)
			event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, projected)
			require.Equal(t, status, event.Details.Get("status").Raw)
			require.Contains(t, event.Details.Raw, `"annotations":null,"annotations":[],"future":1,"future":2`)
			require.True(t, event.Details.Get("extension").Bool())
		})
	}
}

func TestAgentsReasoningAmbiguityDoesNotMutateState(t *testing.T) {
	base := agentsReasoningEvent("text.done", `"text":"Snapshot"`).Raw
	part := agentsReasoningEvent("part.done", `"part":{"type":"summary_text","text":"Snapshot"},"status":null`).Raw
	cases := map[string]string{
		"type":          strings.Replace(base, `"type":`, `"type":"future.event","type":`, 1),
		"escaped_type":  strings.Replace(base, `"type":`, `"ty\u0070e":"agent.session.turn.reasoning_summary_text.done","type":`, 1),
		"event_id":      strings.Replace(base, `"event_id":`, `"event_id":"other","event_id":`, 1),
		"session_id":    strings.Replace(base, `"session_id":`, `"session_id":"other","session_id":`, 1),
		"turn_id":       strings.Replace(base, `"turn_id":`, `"turn_id":"other","turn_id":`, 1),
		"item_id":       strings.Replace(base, `"item_id":`, `"item_id":"other","item_id":`, 1),
		"summary_index": strings.Replace(base, `"summary_index":`, `"summary_index":1,"summary_index":`, 1),
		"output_index":  strings.Replace(base, `"output_index":`, `"output_index":1,"output_index":`, 1),
		"text":          strings.Replace(base, `"text":`, `"text":"Other","text":`, 1),
		"case_text":     strings.Replace(base, `"text":`, `"TEXT":"Other","text":`, 1),
		"part":          strings.Replace(part, `"part":`, `"part":{"type":"summary_text","text":"Other"},"part":`, 1),
		"part_type":     strings.Replace(part, `"type":"summary_text"`, `"type":"summary_text","type":"future"`, 1),
		"part_text":     strings.Replace(part, `"text":`, `"text":"Other","text":`, 1),
		"status":        strings.Replace(part, `"status":`, `"status":"incomplete","status":`, 1),
	}
	for name, raw := range cases {
		for _, cached := range []string{"text.delta", "text.done"} {
			t.Run(name+"/"+cached, func(t *testing.T) {
				var projector AgentsStreamProjector
				field := `"delta":"Existing"`
				if cached == "text.done" {
					field = `"text":"Existing"`
				}
				_, projected, err := projector.Project(t.Context(), agentsReasoningEvent(cached, field), agentsEventsRoute)
				require.NoError(t, err)
				require.True(t, projected)
				before := projector
				_, projected, err = projector.Project(t.Context(), gjson.Parse(raw), agentsEventsRoute)
				require.NoError(t, err)
				require.False(t, projected)
				require.Equal(t, before, projector)
				recovery, projected, err := projector.Project(t.Context(), agentsReasoningEvent("text.done", `"text":"Valid later snapshot"`), agentsEventsRoute)
				require.NoError(t, err)
				require.True(t, projected)
				require.Equal(t, "Valid later snapshot", recovery.Parts[0].Text)
			})
		}
	}
}

func TestAgentsReasoningMalformedIdentityAndFields(t *testing.T) {
	for _, kind := range []string{"text.delta", "text.done", "part.added", "part.done"} {
		fields := `"delta":"Summary"`
		if kind == "text.done" {
			fields = `"text":"Summary"`
		}
		if strings.HasPrefix(kind, "part.") {
			fields = `"part":{"type":"summary_text","text":"Summary"},"status":null`
		}
		base := agentsReasoningEvent(kind, fields).Raw
		for name, oldNew := range map[string][2]string{
			"null_turn":                {`"turn_id":"turn_1"`, `"turn_id":null`},
			"empty_item":               {`"item_id":"msg_1"`, `"item_id":""`},
			"missing_summary_index":    {`"summary_index":0,`, ``},
			"null_summary_index":       {`"summary_index":0`, `"summary_index":null`},
			"negative_summary_index":   {`"summary_index":0`, `"summary_index":-1`},
			"fractional_summary_index": {`"summary_index":0`, `"summary_index":0.5`},
			"overflow_summary_index":   {`"summary_index":0`, `"summary_index":4294967296`},
			"missing_output_index":     {`"output_index":0,`, ``},
			"null_output_index":        {`"output_index":0`, `"output_index":null`},
			"overflow_output_index":    {`"output_index":0`, `"output_index":4294967296`},
			"malformed_text":           {`"Summary"`, `123`},
		} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				var projector AgentsStreamProjector
				_, projected, err := projector.Project(t.Context(), gjson.Parse(strings.Replace(base, oldNew[0], oldNew[1], 1)), agentsEventsRoute)
				require.NoError(t, err)
				require.False(t, projected)
				require.Equal(t, AgentsStreamProjector{}, projector)
			})
		}
	}
}

func TestAgentsReasoningCapacityAndCancellation(t *testing.T) {
	t.Run("capacity", func(t *testing.T) {
		var projector AgentsStreamProjector
		for i := range agentsTrackedParts {
			value := agentsReasoningEvent("text.delta", `"delta":"Existing"`)
			value = gjson.Parse(strings.Replace(value.Raw, `"summary_index":0`, fmt.Sprintf(`"summary_index":%d`, i), 1))
			_, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, projected)
		}
		before := projector
		value := gjson.Parse(strings.Replace(agentsReasoningEvent("text.done", `"text":"Overflow"`).Raw, `"summary_index":0`, `"summary_index":256`, 1))
		_, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
		require.NoError(t, err)
		require.False(t, projected)
		require.Equal(t, before, projector)
		_, projected, err = projector.Project(t.Context(), agentsReasoningEvent("text.done", `"text":"Existing complete"`), agentsEventsRoute)
		require.NoError(t, err)
		require.True(t, projected)
	})
	t.Run("cancel_before_commit", func(t *testing.T) {
		cancelled := 0
		for after := int32(1); after <= 4; after++ {
			var projector AgentsStreamProjector
			ctx, cancel := context.WithCancel(t.Context())
			controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
			_, _, err := projector.Project(controlled, agentsReasoningEvent("text.done", `"text":"Summary"`), agentsEventsRoute)
			if err != nil {
				cancelled++
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, AgentsStreamProjector{}, projector)
			}
			cancel()
		}
		require.GreaterOrEqual(t, cancelled, 3, "check cancellation after staged state mutation")
	})
}
