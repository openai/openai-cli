package transformers

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectAudioStreamPreservesMetadataAndDeduplicatesText(t *testing.T) {
	var out bytes.Buffer
	writer := readable.NewStreamWriter(&out)
	for _, input := range []string{
		`{"type":"transcript.text.delta","delta":"Hello "}`,
		`{"type":"transcript.text.delta","delta":"世界"}`,
		`{"type":"transcript.text.done","text":"Hello 世界","usage":{"total_tokens":9007199254740993},"languages":[{"language":"en","confidence":0.9}],"logprobs":[{"token":"Hello","logprob":-0.1}],"future":null}`,
	} {
		value := gjson.Parse(input)
		event, ok := ProjectAudioStream(value, streamTestRoute("audio.transcriptions"))
		require.True(t, ok)
		value.ForEach(func(key, field gjson.Result) bool {
			if key.Str != "type" && key.Str != "text" && key.Str != "delta" {
				require.Equal(t, field.Raw, event.Details.Get(key.Str).Raw)
			}
			return true
		})
		require.NoError(t, writer.Write(event))
		require.Equal(t, input, value.Raw)
	}
	require.NoError(t, writer.Finish())
	require.Equal(t, 1, strings.Count(out.String(), "Hello 世界"))
	require.Contains(t, out.String(), "9007199254740993")
}

func TestProjectAudioStreamRetainsDeltaDetails(t *testing.T) {
	value := gjson.Parse(`{"type":"transcript.text.delta","delta":"Hello","logprobs":[{"token":"Hello","logprob":-0.1}],"future":null}`)
	event, ok := ProjectAudioStream(value, streamTestRoute("audio.transcriptions"))
	require.True(t, ok)
	require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: "Hello"}}, event.Parts)
	require.Equal(t, `{"logprobs":[{"token":"Hello","logprob":-0.1}],"future":null}`, event.Details.Raw)
}

func TestProjectAudioStreamLeavesDiarizedAndUnknownEventsWhole(t *testing.T) {
	for _, input := range []string{
		`{"type":"transcript.text.delta","delta":"Hello","segment_id":"seg_synthetic"}`,
		`{"type":"transcript.text.segment","id":"seg_synthetic","text":"Hello","speaker":"A","start":0,"end":1,"future":null}`,
		`{"type":"transcript.text.delta","delta":4}`,
		`{"type":"transcript.text.done","text":null}`,
		`{"type":"transcript.text.done","text":"Hello"`,
		`{"type":"future.event","text":"Hello","future":9007199254740993}`,
		`{"type":"error","message":"synthetic-private"}`, `null`, `[]`,
	} {
		value := gjson.Parse(input)
		event, ok := ProjectAudioStream(value, streamTestRoute("audio.transcriptions"))
		require.False(t, ok, input)
		require.Equal(t, readable.StreamEvent{}, event)
		require.Equal(t, input, value.Raw)
	}
	for _, route := range []Route{
		streamTestRoute("audio.translations"), streamTestRoute("audio.speech"),
		streamTestRoute("responses"), streamTestRoute("future"),
		{"(resource) audio.transcriptions > (method) retrieve", OutputStreamEvent},
		audioResponseRoute("audio.transcriptions"), {},
	} {
		_, ok := ProjectAudioStream(gjson.Parse(`{"type":"transcript.text.delta","delta":"Hello"}`), route)
		require.False(t, ok, route)
	}
}

func TestProjectAudioStreamPreservesLargeTranscript(t *testing.T) {
	text := strings.Repeat("世界🌍\n", 100000)
	encoded, err := json.Marshal(text)
	require.NoError(t, err)
	event, ok := ProjectAudioStream(gjson.Parse(`{"type":"transcript.text.done","text":`+string(encoded)+`,"future":9007199254740993}`), streamTestRoute("audio.transcriptions"))
	require.True(t, ok)
	require.Equal(t, text, event.Parts[0].Text)
	require.Equal(t, "9007199254740993", event.Details.Get("future").Raw)
}

func TestSpeechEventFieldsSummarizeOnlyKnownAudio(t *testing.T) {
	input := `{"type":"speech.audio.delta","audio":"SGVsbG8=","future":9007199254740993,"metadata":{"audio":"SGVsbG8="}}`
	value := gjson.Parse(input)
	result, err := summarizeFields(context.Background(), value, speechEventFields(value), false)
	require.NoError(t, err)
	require.Contains(t, result.Get("audio").Str, "8 base64 characters")
	require.Equal(t, "9007199254740993", result.Get("future").Raw)
	require.Equal(t, "SGVsbG8=", result.Get("metadata.audio").Str)
	require.Equal(t, input, value.Raw)
	for _, input := range []string{
		`{"type":"speech.audio.done","audio":"SGVsbG8="}`,
		`{"type":"future.event","audio":"SGVsbG8="}`,
		`{"audio":"SGVsbG8="}`,
	} {
		require.Empty(t, speechEventFields(gjson.Parse(input)))
	}
	for _, input := range []string{
		`{"type":"speech.audio.delta","audio":"not base64!"}`,
		`{"type":"speech.audio.delta","audio":null}`,
		`{"type":"speech.audio.delta","audio":{"future":true}}`,
	} {
		value := gjson.Parse(input)
		result, err := summarizeFields(context.Background(), value, speechEventFields(value), false)
		require.NoError(t, err)
		require.Equal(t, input, result.Raw)
	}
}
