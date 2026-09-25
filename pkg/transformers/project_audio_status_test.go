package transformers

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAudioStreamCompletionRequiresTerminalEvent(t *testing.T) {
	for _, test := range []struct {
		resource, start, done, message string
	}{
		{"audio.transcriptions", `{"type":"transcript.text.delta","delta":"Partial"}`, `{"type":"transcript.text.done","text":"Partial"}`, "the stream ended before the transcription completed"},
		{"audio.transcriptions", `{"type":"transcript.text.segment","text":"Partial","speaker":"A","start":0,"end":1}`, `{"type":"transcript.text.done","text":"Partial"}`, "the stream ended before the transcription completed"},
		{"audio.speech", `{"type":"speech.audio.delta","audio":"SGVsbG8="}`, `{"type":"speech.audio.done","usage":{"output_tokens":1}}`, "the stream ended before the speech audio completed"},
	} {
		t.Run(test.start, func(t *testing.T) {
			var state StreamCompletionState
			route := streamTestRoute(test.resource)
			require.Empty(t, state.CompletionError(route))
			state.Observe(gjson.Parse(test.start), route)
			require.Equal(t, test.message, state.CompletionError(route))
			state.Observe(gjson.Parse(`{"type":"future.event","text":"synthetic-private"}`), route)
			require.Equal(t, test.message, state.CompletionError(route))
			state.Observe(gjson.Parse(test.done), route)
			require.Empty(t, state.CompletionError(route))
		})
	}
}

func TestAudioStreamCompletionKeepsMalformedTranscriptPending(t *testing.T) {
	for _, input := range []string{
		`{"type":"transcript.text.done"}`, `{"type":"transcript.text.done","text":4}`,
		`{"type":"transcript.text.done","text":"Hello"`,
	} {
		var state StreamCompletionState
		route := streamTestRoute("audio.transcriptions")
		state.Observe(gjson.Parse(`{"type":"transcript.text.delta","delta":"Hello"}`), route)
		state.Observe(gjson.Parse(input), route)
		require.NotEmpty(t, state.CompletionError(route))
	}
}

func TestAudioStreamStatusUsesExactRoutes(t *testing.T) {
	for _, resource := range []string{"audio.transcriptions", "audio.speech"} {
		route := streamTestRoute(resource)
		require.Equal(t, "the API reported an error while streaming", StreamFailure(gjson.Parse(`{"type":"error","message":"synthetic-private"}`), route))
		var state StreamCompletionState
		for _, input := range []string{
			`null`, `{}`, `{"type":"future.event","text":"Hello"}`,
			`{"type":"response.failed","error":"synthetic-private"}`,
			`{"event":"error","text":"Hello"}`,
			`{"type":"future.event","error":{"message":"synthetic-private"},"status":"failed"}`,
		} {
			require.Empty(t, StreamFailure(gjson.Parse(input), route))
			state.Observe(gjson.Parse(input), route)
			require.Empty(t, state.CompletionError(route))
		}
	}
	for _, route := range []Route{
		streamTestRoute("audio.translations"), streamTestRoute("future"),
		audioResponseRoute("audio.transcriptions"), audioResponseRoute("audio.speech"),
		{"(resource) audio.speech > (method) retrieve", OutputStreamEvent}, {},
	} {
		require.Empty(t, StreamFailure(gjson.Parse(`{"type":"error"}`), route))
		var state StreamCompletionState
		for _, kind := range []string{"transcript.text.delta", "speech.audio.delta"} {
			state.Observe(gjson.Parse(`{"type":"`+kind+`"}`), route)
			require.Empty(t, state.CompletionError(route))
		}
	}
}
