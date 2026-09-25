package transformers

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStreamCompletionRequiresResponseTerminalAfterKnownProgress(t *testing.T) {
	for _, kind := range []string{"response.created", "response.in_progress", "response.output_text.delta", "response.output_text.done", "response.output_item.done", "response.function_call_arguments.delta"} {
		t.Run(kind, func(t *testing.T) {
			var state StreamCompletionState
			route := streamTestRoute("responses")
			require.Empty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"type":"`+kind+`","delta":"Partial"}`), route)
			require.NotEmpty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"type":"future.event"}`), route)
			require.NotEmpty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"type":"response.completed","response":{"status":"completed","output":[]}}`), route)
			require.Empty(t, state.CompletionError(route))
		})
	}
	for _, kind := range []string{"response.failed", "response.incomplete", "response.cancelled", "response.canceled", "response.done"} {
		var state StreamCompletionState
		route := streamTestRoute("beta.responses")
		state.Observe(gjson.Parse(`{"type":"response.created"}`), route)
		state.Observe(gjson.Parse(`{"type":"`+kind+`"}`), route)
		require.Empty(t, state.CompletionError(route))
	}
}

func TestStreamCompletionTracksEveryChoice(t *testing.T) {
	for _, resource := range []string{"chat.completions", "completions"} {
		t.Run(resource, func(t *testing.T) {
			var state StreamCompletionState
			route := streamTestRoute(resource)
			state.Observe(gjson.Parse(`{"choices":[{"index":0,"finish_reason":null},{"index":2,"finish_reason":null}]}`), route)
			require.NotEmpty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"choices":[{"index":2,"finish_reason":"stop"}]}`), route)
			require.NotEmpty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"choices":[],"usage":{"total_tokens":9}}`), route)
			require.NotEmpty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"choices":[{"index":0,"finish_reason":"length"}]}`), route)
			require.Empty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"choices":[],"usage":{"total_tokens":9}}`), route)
			require.Empty(t, state.CompletionError(route))
		})
	}
}

func TestStreamCompletionLeavesUnknownAndEmptyStreamsUnchanged(t *testing.T) {
	for _, route := range []Route{streamTestRoute("responses"), streamTestRoute("chat.completions"), streamTestRoute("completions"), streamTestRoute("future")} {
		var state StreamCompletionState
		require.Empty(t, state.CompletionError(route))
		for _, input := range []string{`null`, `{}`, `{"type":"future.delta","delta":"text"}`, `{"object":"future","choices":[{"index":0,"finish_reason":null}]}`} {
			state.Observe(gjson.Parse(input), route)
			require.Empty(t, state.CompletionError(route))
		}
	}
	var state StreamCompletionState
	state.Observe(gjson.Parse(`{"type":"response.output_text.delta","delta":"Partial"}`), streamTestRoute("responses"))
	require.Empty(t, state.CompletionError(streamTestRoute("future")))
	state.Observe(gjson.Parse(`{"type":"response.completed"}`), streamTestRoute("responses"))
	require.Empty(t, state.CompletionError(streamTestRoute("responses")))
}
