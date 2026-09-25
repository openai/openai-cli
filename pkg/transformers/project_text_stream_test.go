package transformers

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func streamTestRoute(resource string) Route {
	return Route{"(resource) " + resource + " > (method) create", OutputStreamEvent}
}

func TestProjectResponseTextStream(t *testing.T) {
	for _, test := range []struct {
		name, input string
		parts       []readable.StreamPart
		details     string
	}{
		{"delta", `{"type":"response.output_text.delta","delta":"Hello ","output_index":0,"content_index":0,"item_id":"msg_synthetic","sequence_number":1,"obfuscation":"padding"}`,
			[]readable.StreamPart{{Key: "response:0:0", Text: "Hello "}}, ""},
		{"snapshot", `{"type":"response.output_text.done","text":"Hello world","output_index":2,"content_index":1}`,
			[]readable.StreamPart{{Key: "response:2:1", Text: "Hello world", Snapshot: true, Label: "Output 2, part 1"}}, ""},
		{"refusal delta", `{"type":"response.refusal.delta","delta":"Cannot "}`,
			[]readable.StreamPart{{Key: "response:0:0", Text: "Cannot ", Label: "Refusal"}}, ""},
		{"refusal snapshot", `{"type":"response.refusal.done","refusal":"Cannot help","output_index":1,"content_index":0}`,
			[]readable.StreamPart{{Key: "response:1:0", Text: "Cannot help", Snapshot: true, Label: "Output 1, part 0: Refusal"}}, ""},
		{"part snapshot", `{"type":"response.content_part.done","part":{"type":"output_text","text":"Hello","annotations":[]}}`,
			[]readable.StreamPart{{Key: "response:0:0", Text: "Hello", Snapshot: true}}, ""},
		{"empty item", `{"type":"response.output_item.added","item":{"type":"message","role":"assistant","status":"in_progress","content":[]}}`, nil, ""},
		{"empty reasoning", `{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_synthetic","type":"reasoning","summary":[],"content":[],"encrypted_content":null,"status":"completed"}}`, nil, ""},
		{"reasoning content", `{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_synthetic","type":"reasoning","summary":[{"type":"summary_text","text":"Thinking"}],"future":null}}`, nil,
			`{"output_index":0,"item":{"summary":[{"type":"summary_text","text":"Thinking"}],"future":null}}`},
		{"empty response", `{"type":"response.created","response":{"id":"resp_synthetic","object":"response","status":"in_progress","output":[],"usage":null,"error":null,"incomplete_details":null}}`, nil, ""},
		{"annotations and logprobs", `{"type":"response.output_text.done","text":"Hello","annotations":[{"type":"url_citation","url":"https://example.test"}],"logprobs":[{"token":"Hello","logprob":-0.1}]}`,
			[]readable.StreamPart{{Key: "response:0:0", Text: "Hello", Snapshot: true}}, `{"annotations":[{"type":"url_citation","url":"https://example.test"}],"logprobs":[{"token":"Hello","logprob":-0.1}]}`},
		{"future delta fields", `{"type":"response.output_text.delta","delta":"Hello","future":9007199254740993,"future_null":null,"tool_calls":[{"name":"test"}]}`,
			[]readable.StreamPart{{Key: "response:0:0", Text: "Hello"}}, `{"future":9007199254740993,"future_null":null,"tool_calls":[{"name":"test"}]}`},
		{"tool item", `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"call_synthetic","name":"test","arguments":"{}"}}`, nil,
			`{"output_index":0,"item":{"type":"function_call","id":"call_synthetic","name":"test","arguments":"{}"}}`},
		{"incomplete with text", `{"type":"response.incomplete","response":{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]}],"incomplete_details":{"reason":"max_output_tokens"}}}`,
			[]readable.StreamPart{{Key: "response:0:0", Text: "Partial", Snapshot: true}}, `{"response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`},
		{"complete with usage", `{"type":"response.completed","response":{"model":"synthetic","status":"completed","output":[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"total_tokens":9007199254740993},"future":false}}`,
			[]readable.StreamPart{{Key: "response:0:0", Text: "Hello", Snapshot: true}}, `{"response":{"usage":{"total_tokens":9007199254740993},"future":false}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := gjson.Parse(test.input)
			event, ok := ProjectTextStream(value, streamTestRoute("responses"))
			require.True(t, ok)
			require.Equal(t, test.parts, event.Parts)
			if test.details == "" {
				require.Empty(t, event.Details.Raw)
			} else {
				require.Equal(t, test.details, event.Details.Raw)
			}
			require.Equal(t, test.input, value.Raw, "projection must preserve the original API data")
		})
	}
}

func TestProjectResponseStreamPreservesPartPositionsAndResidualContent(t *testing.T) {
	input := `{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Reasoning"}]},{"id":"msg_synthetic","type":"message","content":[{"type":"output_text","text":"One","future":{"count":9007199254740993}},{"type":"output_image","data":"synthetic"},{"type":"refusal","refusal":"No"}],"phase":"final_answer"},{"type":"function_call","name":"tool","arguments":"{}"},{"type":"message","content":[{"type":"output_text","text":"Three"}]}],"usage":{"total_tokens":7}}}`
	event, ok := ProjectTextStream(gjson.Parse(input), streamTestRoute("responses"))
	require.True(t, ok)
	require.Equal(t, []readable.StreamPart{
		{Key: "response:1:0", Text: "One", Snapshot: true, Label: "Output 1, part 0"},
		{Key: "response:1:2", Text: "No", Snapshot: true, Label: "Output 1, part 2: Refusal"},
		{Key: "response:3:0", Text: "Three", Snapshot: true, Label: "Output 3, part 0"},
	}, event.Parts)
	require.Equal(t, "Reasoning", event.Details.Get("response.output.0.summary.0.text").Str)
	require.Equal(t, "9007199254740993", event.Details.Get("response.output.1.content.0.future.count").Raw)
	require.Equal(t, "output_image", event.Details.Get("response.output.1.content.1.type").Str)
	require.Equal(t, "null", event.Details.Get("response.output.1.content.2").Raw)
	require.Equal(t, "final_answer", event.Details.Get("response.output.1.phase").Str)
	require.Equal(t, "tool", event.Details.Get("response.output.2.name").Str)
	require.Equal(t, "null", event.Details.Get("response.output.3").Raw)
	require.Equal(t, int64(7), event.Details.Get("response.usage.total_tokens").Int())
	require.NotContains(t, event.Details.Raw, `"One"`)
	require.NotContains(t, event.Details.Raw, `"Three"`)
}

func TestProjectChoiceTextStream(t *testing.T) {
	for _, test := range []struct {
		name, resource, input string
		parts                 []readable.StreamPart
		details               string
	}{
		{"chat delta", "chat.completions", `{"object":"chat.completion.chunk","id":"chat_synthetic","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello ","refusal":null},"finish_reason":null}]}`,
			[]readable.StreamPart{{Key: "choice:0:content", Text: "Hello "}}, ""},
		{"empty chat", "chat.completions", `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`, nil, ""},
		{"finished chat", "chat.completions", `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`, nil, ""},
		{"multiple choices", "chat.completions", `{"choices":[{"index":1,"delta":{"content":"One"}},{"index":0,"delta":{"refusal":"No"}}]}`,
			[]readable.StreamPart{{Key: "choice:1:content", Text: "One", Label: "Choice 1"}, {Key: "choice:0:refusal", Text: "No", Label: "Refusal"}}, ""},
		{"content and refusal", "chat.completions", `{"choices":[{"index":0,"delta":{"content":"Text","refusal":"No"}}]}`,
			[]readable.StreamPart{{Key: "choice:0:content", Text: "Text"}, {Key: "choice:0:refusal", Text: "No", Label: "Refusal"}}, ""},
		{"snapshot", "chat.completions", `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}`,
			[]readable.StreamPart{{Key: "choice:0:content", Text: "Hello", Snapshot: true}}, ""},
		{"tools with text", "chat.completions", `{"choices":[{"index":2,"delta":{"content":"Working","tool_calls":[{"index":0,"function":{"name":"test","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`,
			[]readable.StreamPart{{Key: "choice:2:content", Text: "Working", Label: "Choice 2"}}, `{"choices":[{"index":2,"delta":{"tool_calls":[{"index":0,"function":{"name":"test","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`},
		{"usage only", "chat.completions", `{"choices":[],"usage":{"total_tokens":9007199254740993}}`, nil, `{"usage":{"total_tokens":9007199254740993}}`},
		{"length reason", "chat.completions", `{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, nil, `{"choices":[{"index":0,"finish_reason":"length"}]}`},
		{"unknown fields", "chat.completions", `{"choices":[{"index":0,"delta":{"content":"Hello","future":null},"logprobs":{"content":[{"token":"Hello"}]},"future":true}],"future":9007199254740993}`,
			[]readable.StreamPart{{Key: "choice:0:content", Text: "Hello"}}, `{"choices":[{"index":0,"delta":{"future":null},"logprobs":{"content":[{"token":"Hello"}]},"future":true}],"future":9007199254740993}`},
		{"legacy delta", "completions", `{"object":"text_completion","choices":[{"index":0,"text":"Hello ","logprobs":null,"finish_reason":null}]}`,
			[]readable.StreamPart{{Key: "choice:0:content", Text: "Hello "}}, ""},
		{"legacy future", "completions", `{"object":"text_completion","choices":[{"index":0,"text":"Hello","future":false,"finish_reason":"length"}],"usage":{"total_tokens":9}}`,
			[]readable.StreamPart{{Key: "choice:0:content", Text: "Hello"}}, `{"choices":[{"index":0,"future":false,"finish_reason":"length"}],"usage":{"total_tokens":9}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			event, ok := ProjectTextStream(gjson.Parse(test.input), streamTestRoute(test.resource))
			require.True(t, ok)
			require.Equal(t, test.parts, event.Parts)
			require.Equal(t, test.details, event.Details.Raw)
		})
	}
}

func TestProjectTextStreamRejectsUnknownAndMalformedShapes(t *testing.T) {
	for _, test := range []struct{ resource, input string }{
		{"responses", `null`},
		{"responses", `{"type":"future.text.delta","delta":"Hello"}`},
		{"responses", `{"type":"transcript.text.delta","delta":"Hello"}`},
		{"responses", `{"type":"response.output_text.delta","delta":{"text":"Hello"}}`},
		{"responses", `{"type":"response.output_text.delta"}`},
		{"responses", `{"type":"response.output_text.delta","delta":"Hello","item_id":"msg_synthetic"}`},
		{"responses", `{"type":"response.output_item.done","item":{"id":"msg_synthetic","type":"message","content":[]}}`},
		{"responses", `{"type":"response.output_item.done"}`},
		{"responses", `{"type":"response.completed","response":null}`},
		{"responses", `{"type":"response.completed","response":{"object":"future","output":[]}}`},
		{"responses", `{"type":"response.content_part.done","part":{"type":"output_text","text":12}}`},
		{"chat.completions", `{"object":"future","choices":[{"delta":{"content":"Hello"}}]}`},
		{"chat.completions", `{"type":"future","choices":[]}`},
		{"chat.completions", `{"choices":{}}`},
		{"chat.completions", `{"choices":[null]}`},
		{"chat.completions", `{"choices":[{"index":0,"delta":{"content":"One"}},{"index":0,"delta":{"content":"Two"}}]}`},
		{"completions", `{"object":"chat.completion.chunk","choices":[]}`},
	} {
		t.Run(test.resource+test.input, func(t *testing.T) {
			event, ok := ProjectTextStream(gjson.Parse(test.input), streamTestRoute(test.resource))
			require.False(t, ok)
			require.Equal(t, readable.StreamEvent{}, event)
		})
	}
	for _, index := range []string{`-1`, `0.5`, `1e2`, `"0"`, `null`, `{}`, `18446744073709551616`} {
		_, ok := ProjectTextStream(gjson.Parse(`{"type":"response.output_text.delta","delta":"Hello","output_index":`+index+`}`), streamTestRoute("responses"))
		require.False(t, ok, index)
		_, ok = ProjectTextStream(gjson.Parse(`{"choices":[{"index":`+index+`,"delta":{"content":"Hello"}}]}`), streamTestRoute("chat.completions"))
		require.False(t, ok, index)
	}
}

func TestProjectTextStreamUsesExactRoutes(t *testing.T) {
	value := gjson.Parse(`{"type":"response.output_text.delta","delta":"Hello"}`)
	for _, resource := range []string{"responses", "beta.responses"} {
		for _, method := range []string{"create", "retrieve"} {
			_, ok := ProjectTextStream(value, Route{"(resource) " + resource + " > (method) " + method, OutputStreamEvent})
			require.True(t, ok)
		}
	}
	for _, route := range []Route{
		{"", OutputStreamEvent},
		{"(resource) responses > (method) create", OutputResponse},
		{"(resource) responses > (method) create", OutputPageItem},
		{"(resource) responses > (method) cancel", OutputStreamEvent},
		{"(resource) chat.completions > (method) retrieve", OutputStreamEvent},
		{"(resource) audio.transcriptions > (method) create", OutputStreamEvent},
		{"(resource) beta.threads.runs > (method) create", OutputStreamEvent},
	} {
		_, ok := ProjectTextStream(value, route)
		require.False(t, ok, route)
	}
}

func TestProjectTextStreamPreservesMalformedKnownContent(t *testing.T) {
	for _, input := range []string{
		`{"choices":[{"index":0,"delta":{"content":{"future":"hello"},"refusal":5}}]}`,
		`{"choices":[{"index":0,"delta":{"role":"future","content":"Hello"}}]}`,
	} {
		event, ok := ProjectTextStream(gjson.Parse(input), streamTestRoute("chat.completions"))
		require.True(t, ok)
		require.Empty(t, event.Parts)
		require.JSONEq(t, input, event.Details.Raw)
	}
}

func TestProjectTextStreamKeepsUnknownFieldsAtTheirAPISlot(t *testing.T) {
	for _, test := range []struct{ resource, input, path string }{
		{"responses", `{"type":"response.content_part.done","part":{"type":"output_text","text":"Hello","usage":null,"status":"completed"}}`, "part"},
		{"responses", `{"type":"response.output_item.done","item":{"type":"message","content":[],"usage":null,"finish_reason":"stop"}}`, "item"},
		{"responses", `{"type":"response.refusal.delta","delta":"No","annotations":null}`, ""},
		{"chat.completions", `{"choices":[{"index":0,"delta":{"content":"Hello","usage":null,"status":"completed"}}]}`, "choices.0.delta"},
	} {
		event, ok := ProjectTextStream(gjson.Parse(test.input), streamTestRoute(test.resource))
		require.True(t, ok)
		residual := event.Details
		if test.path != "" {
			residual = residual.Get(test.path)
		}
		if gjson.Get(test.input, "type").Str == "response.refusal.delta" {
			require.Equal(t, "null", residual.Get("annotations").Raw)
		} else {
			require.Equal(t, "null", residual.Get("usage").Raw)
			require.True(t, residual.Get("status").Exists() || residual.Get("finish_reason").Exists())
		}
	}
}

func TestResponseProjectionAndStreamAssemblyDoNotRepeatSnapshots(t *testing.T) {
	var out bytes.Buffer
	writer := readable.NewStreamWriter(&out)
	for _, input := range []string{
		`{"type":"response.output_text.delta","delta":"Hello ","output_index":0,"content_index":0}`,
		`{"type":"response.output_text.delta","delta":"世界","output_index":0,"content_index":0}`,
		`{"type":"response.output_text.done","text":"Hello 世界","output_index":0,"content_index":0}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello 世界"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"Hello 世界"}]}}`,
		`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hello 世界!"}]}]}}`,
	} {
		event, ok := ProjectTextStream(gjson.Parse(input), streamTestRoute("responses"))
		require.True(t, ok)
		require.NoError(t, writer.Write(event))
	}
	require.NoError(t, writer.Finish())
	require.Equal(t, "Hello 世界!\n", out.String())
}

func TestProjectTextStreamPreservesLargeUnicodeText(t *testing.T) {
	text := strings.Repeat("世界🌍\n", 100000)
	encoded, err := json.Marshal(text)
	require.NoError(t, err)
	input := `{"type":"response.output_text.delta","delta":` + string(encoded) + `,"future":9007199254740993}`
	event, ok := ProjectTextStream(gjson.Parse(input), streamTestRoute("responses"))
	require.True(t, ok)
	require.Equal(t, text, event.Parts[0].Text)
	require.Equal(t, "9007199254740993", event.Details.Get("future").Raw)
}
