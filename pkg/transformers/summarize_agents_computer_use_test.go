package transformers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentsComputerUseEvent(kind, output string) gjson.Result {
	return gjson.Parse(`{"type":"agent.session.turn.item.` + kind + `","event_id":"ev_computer","session_id":"sess_test","turn_id":"turn_test","output_index":0,"item":{"id":"computer_test","type":"computer_use_call","turn_id":"turn_test","title":"Read the synthetic report","status":"completed","output":` + output + `}}`)
}

func TestAgentsComputerUseSummaryRetainsActivityWithoutImageData(t *testing.T) {
	encoded := strings.Repeat("QUJD", 65536)
	for _, kind := range []string{"added", "done"} {
		t.Run(kind, func(t *testing.T) {
			value := agentsComputerUseEvent(kind, `{"type":"computer_screenshot","image_url":`+strconv.Quote("data:image/jpeg;base64,"+encoded)+`}`)
			original := value.Raw
			var projector AgentsStreamProjector
			event, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, projected, "the pinned computer-use item needs a human projection")
			require.True(t, projector.HasOmissions())
			require.Equal(t, "computer_test", event.Details.Get("item.id").Str)
			require.Equal(t, "computer_use_call", event.Details.Get("item.type").Str)
			require.Equal(t, "completed", event.Details.Get("item.status").Str)
			require.Equal(t, "Read the synthetic report", event.Details.Get("item.title").Str)
			require.Equal(t, "sess_test", event.Details.Get("session_id").Str)
			require.Equal(t, "turn_test", event.Details.Get("turn_id").Str)
			require.Equal(t, "computer_screenshot", event.Details.Get("item.output.type").Str)
			require.Equal(t, fmt.Sprintf("(JPEG screenshot; %d base64 characters)", len(encoded)), event.Details.Get("item.output.image_url").Str)
			if strings.Contains(event.Details.Raw, encoded) {
				t.Fatalf("human projection retained %d encoded image characters", len(encoded))
			}
			require.Equal(t, original, value.Raw, "the source event must stay unchanged")
		})
	}
}

func TestAgentsComputerUseSummaryPreservesUnfamiliarFields(t *testing.T) {
	const source = `{"type":"agent.session.turn.item.done","event_id":"ev_computer","session_id":"sess_test","turn_id":"turn_test","future_event":{"number":9007199254740993},"item":{"id":"computer_test","type":"computer_use_call","turn_id":"turn_test","title":"Synthetic title","status":"failed","future_item":null,"output":{"type":"computer_screenshot","image_url":"data:image/jpeg;base64,QUJDRA==","future_output":{"image_url":"preserve unfamiliar data"}}}}`
	for _, value := range []gjson.Result{gjson.Parse(source), gjson.Get(`{"wrapper":`+source+`}`, "wrapper")} {
		var projector AgentsStreamProjector
		event, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
		require.NoError(t, err)
		require.True(t, projected)
		require.True(t, projector.HasOmissions())
		require.Equal(t, "9007199254740993", event.Details.Get("future_event.number").Raw)
		require.Equal(t, "null", event.Details.Get("item.future_item").Raw)
		require.Equal(t, "preserve unfamiliar data", event.Details.Get("item.output.future_output.image_url").Str)
		require.Equal(t, "failed", event.Details.Get("item.status").Str)
		require.Equal(t, "(JPEG screenshot; 8 base64 characters)", event.Details.Get("item.output.image_url").Str)
		require.Equal(t, source, value.Raw)
	}
}

func TestAgentsComputerUseSummaryPreservesUnrecognizedImageValues(t *testing.T) {
	for _, output := range []string{
		`null`,
		`{"type":"computer_screenshot","image_url":"https://synthetic.invalid/image.jpg"}`,
		`{"type":"computer_screenshot","image_url":"data:image/jpeg;base64,not base64!"}`,
		`{"type":"computer_screenshot","image_url":"data:image/jpeg;base64,"}`,
		`{"type":"computer_screenshot","image_url":42}`,
		`{"type":"future_screenshot","image_url":"data:image/jpeg;base64,QUJDRA=="}`,
		`{"type":"computer_screenshot","image_url":"data:image/png;base64,QUJDRA=="}`,
	} {
		t.Run(output, func(t *testing.T) {
			value := agentsComputerUseEvent("done", output)
			event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			if projected {
				if output != "null" {
					require.JSONEq(t, output, event.Details.Get("item.output").Raw)
				}
			} else {
				require.Equal(t, output, value.Get("item.output").Raw)
			}
		})
	}
}

func TestAgentsComputerUseSummaryCancelsDuringImageScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 8}
	value := agentsComputerUseEvent("done", `{"type":"computer_screenshot","image_url":`+strconv.Quote("data:image/jpeg;base64,"+strings.Repeat("QUJD", 65536))+`}`)
	event, projected, err := new(AgentsStreamProjector).Project(controlled, value, agentsEventsRoute)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, projected)
	require.False(t, event.Details.Exists())
}
