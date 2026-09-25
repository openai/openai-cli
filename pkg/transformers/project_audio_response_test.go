package transformers

import (
	"testing"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func audioResponseRoute(resource string) Route {
	return Route{"(resource) " + resource + " > (method) create", OutputResponse}
}

func TestProjectAudioResponsePreservesMetadata(t *testing.T) {
	for _, resource := range []string{"audio.transcriptions", "audio.translations"} {
		for _, input := range []string{
			`{"text":"Hello 世界"}`,
			`{"text":"Hello 世界","task":"transcribe","language":"english","duration":2.5,"words":[{"word":"Hello","start":0,"end":1}],"segments":[{"id":"seg_synthetic","text":"Hello 世界","speaker":"A","start":0,"end":2.5}],"usage":{"total_tokens":9007199254740993},"languages":[{"language":"en","confidence":0.9}],"logprobs":[{"token":"Hello","logprob":-0.1}],"future":null}`,
		} {
			t.Run(resource+input, func(t *testing.T) {
				value := gjson.Parse(input)
				event, ok := ProjectAudioResponse(value, audioResponseRoute(resource))
				require.True(t, ok)
				require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: "Hello 世界", Snapshot: true}}, event.Parts)
				value.ForEach(func(key, field gjson.Result) bool {
					if key.Str != "text" {
						require.Equal(t, field.Raw, event.Details.Get(key.Str).Raw)
					}
					return true
				})
				require.False(t, event.Details.Get("text").Exists())
				require.Equal(t, input, value.Raw)
			})
		}
	}
}

func TestProjectAudioResponsePreservesNativeTextAndSubtitles(t *testing.T) {
	for _, input := range []string{
		`"Hello 世界\n"`,
		`"1\r\n00:00:00,000 --> 00:00:01,000\r\nSynthetic words\r\n"`,
		`"WEBVTT\n\n00:00.000 --> 00:01.000\nSynthetic words\n"`,
		`""`,
	} {
		value := gjson.Parse(input)
		event, ok := ProjectAudioResponse(value, audioResponseRoute("audio.transcriptions"))
		require.True(t, ok)
		require.Equal(t, value.Str, event.Parts[0].Text)
		require.Empty(t, event.Details.Raw)
		require.Equal(t, input, value.Raw)
	}
}

func TestProjectAudioResponseFallsBackForOtherRoutesAndShapes(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `{"text":5}`, `{"text":{"future":true}}`,
		`{"text":"Hello","type":"future"}`, `{"text":"Hello","object":"future"}`,
		`{"text":"Hello","error":{"message":"synthetic-private"}}`,
		`{"text":"Hello","status":"failed"}`, `{"text":"Hello","status":5}`,
		`{"text":"Hello"`, `"Hello`,
	} {
		event, ok := ProjectAudioResponse(gjson.Parse(input), audioResponseRoute("audio.transcriptions"))
		require.False(t, ok, input)
		require.Equal(t, readable.StreamEvent{}, event)
	}
	for _, route := range []Route{
		audioResponseRoute("audio.speech"), audioResponseRoute("future"),
		{"(resource) audio.transcriptions > (method) retrieve", OutputResponse},
		{"(resource) audio.transcriptions > (method) create", OutputPageItem},
		streamTestRoute("audio.transcriptions"), {},
	} {
		_, ok := ProjectAudioResponse(gjson.Parse(`{"text":"Hello"}`), route)
		require.False(t, ok, route)
	}
}
