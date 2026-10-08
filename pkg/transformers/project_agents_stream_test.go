package transformers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentsTextEvent(kind, eventID, turn, item, text string) gjson.Result {
	field := "delta"
	if strings.HasSuffix(kind, "done") {
		field = "text"
	}
	encoded, _ := json.Marshal(map[string]any{
		"type":     "agent.session.turn.output_text." + kind,
		"event_id": eventID, "session_id": "sess_test", "turn_id": turn,
		"item_id": item, "output_index": 0, "content_index": 0, field: text,
	})
	return gjson.ParseBytes(encoded)
}

func agentsRender(t *testing.T, events ...gjson.Result) string {
	t.Helper()
	var projector AgentsStreamProjector
	var output bytes.Buffer
	writer := readable.NewStreamWriter(&output)
	for _, value := range events {
		original := value.Raw
		event, projected, err := projector.Project(context.Background(), value, agentsEventsRoute)
		require.NoError(t, err)
		if projected {
			err = writer.Write(event)
		} else {
			err = writer.WriteValue(value)
		}
		require.NoError(t, err)
		require.Equal(t, original, value.Raw)
	}
	require.NoError(t, writer.Finish())
	return output.String()
}

func TestAgentsTextRenderingDeduplicatesSnapshotsAndLateDeltas(t *testing.T) {
	delta := agentsTextEvent("delta", "ev_1", "turn_1", "msg_1", "Hello ")
	final := agentsTextEvent("done", "ev_2", "turn_1", "msg_1", "Hello world")
	output := agentsRender(t, delta, delta, final, final,
		agentsTextEvent("delta", "ev_late", "turn_1", "msg_1", "late"))
	require.Equal(t, "Agent:\nHello world\n", output)
}

func TestAgentsTextChangedDuplicateIDAndInterleavedIdentities(t *testing.T) {
	output := agentsRender(t,
		agentsTextEvent("delta", "same", "turn_1", "msg_1", "Hello "),
		agentsTextEvent("delta", "same", "turn_1", "msg_1", "world"),
		agentsTextEvent("done", "ev_2", "turn_2", "msg_1", "Other turn"),
		agentsTextEvent("done", "ev_3", "turn_1", "msg_1", "Hello world"))
	require.Equal(t, 1, strings.Count(output, "Hello world"))
	require.Contains(t, output, "Other turn")
}

func TestAgentsTextSnapshotsEscapeControlsAndRetainLargeText(t *testing.T) {
	text := strings.Repeat("hello世界", 1024*128) + "\x1b[2J"
	output := agentsRender(t, agentsTextEvent("done", "ev_final", "turn", "item", text))
	require.NotContains(t, output, "\x1b")
	require.Contains(t, output, `\u001b[2J`)
	require.Equal(t, strings.Count(text, "hello世界"), strings.Count(output, "hello世界"))
}

func TestAgentsMessageSnapshotDoesNotRepeatTextAndKeepsAnnotations(t *testing.T) {
	output := agentsRender(t,
		agentsTextEvent("delta", "ev_1", "turn_1", "msg_1", "Hello "),
		gjson.Parse(`{"type":"agent.session.turn.item.done","event_id":"ev_done","session_id":"sess_test","turn_id":"turn_1","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"Hello world","annotations":[{"type":"artifact","artifact_id":"artifact_synthetic"}]}]}}`),
		agentsTextEvent("done", "ev_2", "turn_1", "msg_1", "Hello world"),
		agentsTextEvent("delta", "late", "turn_1", "msg_1", "late"))
	require.Equal(t, 1, strings.Count(output, "Hello world"))
	require.NotContains(t, output, "late")
	require.Contains(t, output, "artifact_synthetic")
	require.Contains(t, output, "final_answer")
}

func TestAgentsContentPartSnapshotsAndLateAddedItems(t *testing.T) {
	output := agentsRender(t,
		agentsTextEvent("delta", "ev_1", "turn_1", "msg_1", "Hello "),
		gjson.Parse(`{"type":"agent.session.turn.content_part.done","event_id":"ev_done","session_id":"sess_test","turn_id":"turn_1","item_id":"msg_1","content_index":0,"part":{"type":"output_text","text":"Hello world","annotations":[]}}`),
		gjson.Parse(`{"type":"agent.session.turn.content_part.added","event_id":"ev_late","session_id":"sess_test","turn_id":"turn_1","item_id":"msg_1","content_index":0,"part":{"type":"output_text","text":"Old","annotations":[]}}`),
		gjson.Parse(`{"type":"agent.session.turn.item.done","event_id":"ev_item","session_id":"sess_test","turn_id":"turn_1","item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello world"}]}}`))
	require.Equal(t, "Agent:\nHello world\n", output)
}

func TestAgentsFutureFieldsAndToolFailuresRemainVisible(t *testing.T) {
	output := agentsRender(t,
		gjson.Parse(`{"type":"future.agents.event","event_id":"future","arbitrary":9007199254740993}`),
		gjson.Parse(`{"type":"agent.session.turn.item.done","item":{"type":"command_execution","status":"failed","error":{"code":"synthetic_failure"}}}`),
		gjson.Parse(`{"type":"agent.session.turn.output_text.done","event_id":"future_field","session_id":"sess_test","turn_id":"turn","item_id":"msg","content_index":0,"text":"Answer","new_field":null}`))
	require.Contains(t, output, "future.agents.event")
	require.Contains(t, output, "9007199254740993")
	require.Contains(t, output, "command_execution")
	require.Contains(t, output, "synthetic_failure")
	require.Contains(t, output, "New field")
}

func TestAgentsProjectorFallsBackAtCapacityWithoutRejectingEvents(t *testing.T) {
	var projector AgentsStreamProjector
	for i := 0; i < agentsTrackedParts; i++ {
		_, ok, err := projector.Project(context.Background(), agentsTextEvent("done", fmt.Sprint(i), "turn", fmt.Sprint(i), "answer"), agentsEventsRoute)
		require.NoError(t, err)
		require.True(t, ok)
	}
	value := agentsTextEvent("done", "extra", "turn", "extra", "preserved answer")
	for range 2 {
		_, ok, err := projector.Project(context.Background(), value, agentsEventsRoute)
		require.NoError(t, err)
		require.False(t, ok)
	}
	require.Equal(t, agentsTrackedParts, projector.partCount)
	require.LessOrEqual(t, projector.recentSize, agentsRecentEvents)
}

func TestAgentsProjectionCancellationAndRouteIsolation(t *testing.T) {
	var projector AgentsStreamProjector
	value := agentsTextEvent("done", "ev", "turn", "item", "secret-free synthetic text")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := projector.Project(ctx, value, agentsEventsRoute)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, projector.partCount)
	_, projected, err := projector.Project(context.Background(), value, streamTestRoute("responses"))
	require.NoError(t, err)
	require.False(t, projected)
}
