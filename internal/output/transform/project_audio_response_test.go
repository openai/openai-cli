package transform

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
				if value.Get("segments").Exists() {
					require.Equal(t, `[{"id":"seg_synthetic","text":"Hello 世界","speaker":"A","start":"00:00.000 (0 s)","end":"00:02.500 (2.5 s)"}]`, event.Details.Get("segments").Raw)
				}
				require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: "Hello 世界", Snapshot: true}}, event.Parts)
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
		name, input  string
		starts, ends []string
	}{
		{"fractional and hours", `{"text":"One. Two.","segments":[{"text":"One.","start":0.0014,"end":59.9996,"speaker":"A"},{"text":" Two.","start":3600.005,"end":3723.456,"speaker":"世界"}]}`,
			[]string{"00:00.001 (0.0014 s)", "01:00:00.005 (3600.005 s)"}, []string{"01:00.000 (59.9996 s)", "01:02:03.456 (3723.456 s)"}},
		{"overlap retains order", `{"text":"Later Earlier","segments":[{"text":"Later","start":9,"end":11},{"text":"Earlier","start":8,"end":10}]}`,
			[]string{"00:09.000 (9 s)", "00:08.000 (8 s)"}, []string{"00:11.000 (11 s)", "00:10.000 (10 s)"}},
		{"missing and reversed", `{"text":"One Two Three","segments":[{"text":"One","end":3},{"text":"Two","start":5,"end":2,"speaker":7},{"text":"Three","start":"0","end":null}]}`, nil, nil},
		{"negative and huge", `{"text":"One Two","segments":[{"text":"One","start":-1,"end":1},{"text":"Two","start":1e100,"end":2e100}]}`, nil, nil},
		{"details keep positions", `{"text":"A B","segments":[{"text":"A","start":0,"end":1},{"text":"B","speaker":"B","start":1,"end":2,"confidence":0.99,"future":9007199254740993}],"words":[{"word":"A","start":0}],"future":null}`,
			[]string{"00:00.000 (0 s)", "00:01.000 (1 s)"}, []string{"00:01.000 (1 s)", "00:02.000 (2 s)"}},
		{"boundary whitespace", `{"text":"  Hello  world\nNext\n","segments":[{"text":" Hello  world "},{"text":"\nNext\n"}]}`, nil, nil},
		{"empty text with times", `{"text":"","segments":[{"text":"","start":0,"end":0,"speaker":""}]}`,
			[]string{"00:00.000 (0 s)"}, []string{"00:00.000 (0 s)"}},
		{"exact fractional precision", `{"text":"One","segments":[{"text":"One","start":0.100000000000000001,"end":0.100499999999999999}]}`,
			[]string{"00:00.100 (0.100000000000000001 s)"}, []string{"00:00.100 (0.100499999999999999 s)"}},
		{"exact reversed range", `{"text":"One","segments":[{"text":"One","start":0.100000000000000001,"end":0.1}]}`, nil, nil},
		{"escaped boundary controls", `{"text":"\r\u2028One\u2029\r","segments":[{"text":"\r\u2028One\u2029\r","start":0,"end":1}]}`,
			[]string{"00:00.000 (0 s)"}, []string{"00:01.000 (1 s)"}},
		{"extreme representations stay readable", `{"text":"One Two","segments":[{"text":"One","start":0e999999999,"end":1},{"text":"Two","start":1e-999999999,"end":1}]}`, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := gjson.Parse(tc.input)
			event, ok := ProjectAudioResponse(value, audioResponseRoute("audio.transcriptions"))
			require.True(t, ok)
			require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: value.Get("text").Str, Snapshot: true}}, event.Parts)
			original, projected := value.Get("segments").Array(), event.Details.Get("segments").Array()
			require.Len(t, projected, len(original))
			for index, segment := range original {
				segment.ForEach(func(key, field gjson.Result) bool {
					if tc.starts != nil && (key.Str == "start" || key.Str == "end") {
						want := tc.starts[index]
						if key.Str == "end" {
							want = tc.ends[index]
						}
						require.Equal(t, gjson.String, projected[index].Get(key.Str).Type)
						require.Equal(t, want, projected[index].Get(key.Str).Str)
					} else {
						require.Equal(t, field.Raw, projected[index].Get(key.Str).Raw)
					}
					return true
				})
			}
			require.Equal(t, tc.input, value.Raw)
		})
	}
}

func TestProjectAudioResponseKeepsAggregateAndGroupedDetails(t *testing.T) {
	for _, transcript := range []string{"Segment", "Complete transcript with extra words", "A: Segment", "Seg  ment"} {
		input := `{"text":"` + transcript + `","segments":[{"id":"seg_one","text":"Segment","start":0,"end":1,"speaker":"A","confidence":0.99}]}`
		event, ok := ProjectAudioResponse(gjson.Parse(input), audioResponseRoute("audio.transcriptions"))
		require.True(t, ok)
		require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: transcript, Snapshot: true}}, event.Parts)
		require.Equal(t, `[{"id":"seg_one","text":"Segment","start":"00:00.000 (0 s)","end":"00:01.000 (1 s)","speaker":"A","confidence":0.99}]`, event.Details.Get("segments").Raw)
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

func TestProjectAudioResponseKeepsDuplicateSegmentArrays(t *testing.T) {
	const arrays = `"segments":[{"text":"Hello","start":0,"end":1}],"segments":[{"text":"Later","future":7}]`
	event, ok := ProjectAudioResponse(gjson.Parse(`{"text":"Hello",`+arrays+`}`), audioResponseRoute("audio.transcriptions"))
	require.True(t, ok)
	require.Equal(t, []readable.StreamPart{{Key: "transcript", Text: "Hello", Snapshot: true}}, event.Parts)
	require.Equal(t, `{`+arrays+`}`, event.Details.Raw)
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
