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
				wantText := "Hello 世界"
				if value.Get("segments").Exists() {
					wantText = "[00:00.000–00:02.500] A: Hello 世界"
					require.Equal(t, `[{"id":"seg_synthetic"}]`, event.Details.Get("segments").Raw)
				}
				require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: wantText, Snapshot: true}}, event.Parts)
				value.ForEach(func(key, field gjson.Result) bool {
					if key.Str != "text" && key.Str != "segments" {
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

func TestProjectAudioResponseSegmentTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name, input, text, details string
	}{
		{"fractional and hours", `{"text":"One. Two.","segments":[{"text":"One.","start":0.0014,"end":59.9996,"speaker":"A"},{"text":" Two.","start":3600.005,"end":3723.456,"speaker":"世界"}]}`,
			"[00:00.001–01:00.000] A: One.\n[01:00:00.005–01:02:03.456] 世界: Two.", `{"segments":[{"start":0.0014,"end":59.9996},null]}`},
		{"overlap retains order", `{"text":"Later Earlier","segments":[{"text":"Later","start":9,"end":11},{"text":"Earlier","start":8,"end":10}]}`,
			"[00:09.000–00:11.000] Later\n[00:08.000–00:10.000] Earlier", ""},
		{"missing and reversed", `{"text":"One Two Three","segments":[{"text":"One","end":3},{"text":"Two","start":5,"end":2,"speaker":7},{"text":"Three","start":"0","end":null}]}`,
			"One\nTwo\nThree", `{"segments":[{"end":3},{"start":5,"end":2,"speaker":7},{"start":"0","end":null}]}`},
		{"negative and huge", `{"text":"One Two","segments":[{"text":"One","start":-1,"end":1},{"text":"Two","start":1e100,"end":2e100}]}`,
			"One\nTwo", `{"segments":[{"start":-1,"end":1},{"start":1e100,"end":2e100}]}`},
		{"details keep positions", `{"text":"A B","segments":[{"text":"A","start":0,"end":1},{"text":"B","speaker":"B","start":1,"end":2,"confidence":0.99,"future":9007199254740993}],"words":[{"word":"A","start":0}],"future":null}`,
			"[00:00.000–00:01.000] A\n[00:01.000–00:02.000] B: B", `{"segments":[null,{"confidence":0.99,"future":9007199254740993}],"words":[{"word":"A","start":0}],"future":null}`},
		{"boundary whitespace", `{"text":"  Hello  world\nNext\n","segments":[{"text":" Hello  world "},{"text":"\nNext\n"}]}`,
			"Hello  world\nNext", ""},
		{"empty text with times", `{"text":"","segments":[{"text":"","start":0,"end":0,"speaker":""}]}`,
			"[00:00.000–00:00.000] ", `{"segments":[{"speaker":""}]}`},
		{"exact fractional precision", `{"text":"One","segments":[{"text":"One","start":0.100000000000000001,"end":0.100499999999999999}]}`,
			"[00:00.100–00:00.100] One", `{"segments":[{"start":0.100000000000000001,"end":0.100499999999999999}]}`},
		{"exact reversed range", `{"text":"One","segments":[{"text":"One","start":0.100000000000000001,"end":0.1}]}`,
			"One", `{"segments":[{"start":0.100000000000000001,"end":0.1}]}`},
		{"escaped boundary controls", `{"text":"\r\u2028One\u2029\r","segments":[{"text":"\r\u2028One\u2029\r","start":0,"end":1}]}`,
			"[00:00.000–00:01.000] \r\u2028One\u2029\r", ""},
		{"extreme representations stay readable", `{"text":"One Two","segments":[{"text":"One","start":0e999999999,"end":1},{"text":"Two","start":1e-999999999,"end":1}]}`,
			"One\nTwo", `{"segments":[{"start":0e999999999,"end":1},{"start":1e-999999999,"end":1}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := gjson.Parse(tc.input)
			event, ok := ProjectAudioResponse(value, audioResponseRoute("audio.transcriptions"))
			require.True(t, ok)
			require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: tc.text, Snapshot: true}}, event.Parts)
			require.Equal(t, tc.details, event.Details.Raw)
			require.Equal(t, tc.input, value.Raw)
		})
	}
}

func TestProjectAudioResponseKeepsDifferentAggregate(t *testing.T) {
	for _, transcript := range []string{"Complete transcript with extra words", "A: Segment", "Seg  ment"} {
		input := `{"text":"` + transcript + `","segments":[{"text":"Segment","start":0,"end":1,"speaker":"A"}]}`
		event, ok := ProjectAudioResponse(gjson.Parse(input), audioResponseRoute("audio.transcriptions"))
		require.True(t, ok)
		require.Equal(t, []readable.StreamPart{
			{Key: "transcript", Text: transcript, Snapshot: true},
			{Key: "transcript:segments", Label: "Segments", Text: "[00:00.000–00:01.000] A: Segment", Snapshot: true},
		}, event.Parts)
	}
}

func TestProjectAudioResponseKeepsMalformedSegmentArrays(t *testing.T) {
	for _, segments := range []string{
		`null`, `{}`, `[]`, `42`, `[null]`, `["text"]`, `[{"text":null}]`,
		`[{"text":"Good","start":0,"end":1},{"text":5}]`,
		`[{"text":"First","text":"Second","start":0,"end":1}]`,
		`[{"text":"Good","speaker":"A","speaker":"B","start":0,"end":1}]`,
		`[{"text":"Good","start":0,"start":2,"end":1}]`,
	} {
		input := `{"text":"Full transcript","segments":` + segments + `}`
		event, ok := ProjectAudioResponse(gjson.Parse(input), audioResponseRoute("audio.transcriptions"))
		require.True(t, ok)
		require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: "Full transcript", Snapshot: true}}, event.Parts)
		require.Equal(t, segments, event.Details.Get("segments").Raw)
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
