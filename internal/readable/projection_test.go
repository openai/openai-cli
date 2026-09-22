package readable

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReadableResponses(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		input     string
		text      string
	}{
		{
			name:  "response text",
			input: `{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]}]}]}`,
			text:  "Hello",
		},
		{
			name:  "multiple blocks and messages",
			input: `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"One"},{"type":"output_text","text":"Two"}]},{"type":"message","content":[{"type":"refusal","refusal":"Cannot help"}]}]}`,
			text:  "One\n\nTwo\n\nCannot help",
		},
		{
			name:  "empty reasoning metadata",
			input: `{"object":"response","output":[{"id":"rs_fake","type":"reasoning","summary":[]},{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}`,
			text:  "Answer",
		},
		{
			name:      "response scoped by route",
			operation: "(resource) responses > (method) retrieve",
			input:     `{"output":[{"type":"message","content":[{"type":"output_text","text":"Retrieved"}]}]}`,
			text:      "Retrieved",
		},
		{
			name:  "chat completion",
			input: `{"object":"chat.completion","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Hello","refusal":null,"tool_calls":[]}}]}`,
			text:  "Hello",
		},
		{
			name:  "chat refusal",
			input: `{"object":"chat.completion","choices":[{"message":{"content":null,"refusal":"Cannot help"}}]}`,
			text:  "Cannot help",
		},
		{
			name:  "empty chat refusal adds no separator",
			input: `{"object":"chat.completion","choices":[{"message":{"content":"Hello","refusal":""}}]}`,
			text:  "Hello",
		},
		{
			name:      "chat scoped by route",
			operation: "(resource) chat.completions > (method) create",
			input:     `{"choices":[{"message":{"content":"Hello"}}]}`,
			text:      "Hello",
		},
		{
			name:  "completion",
			input: `{"object":"text_completion","choices":[{"text":"Hello","finish_reason":"stop"}]}`,
			text:  "Hello",
		},
		{
			name:      "completion scoped by route",
			operation: "(resource) completions > (method) create",
			input:     `{"choices":[{"text":"Hello"}]}`,
			text:      "Hello",
		},
		{
			name:      "audio transcription",
			operation: "(resource) audio.transcriptions > (method) create",
			input:     `{"text":"Spoken words","usage":{"type":"tokens","total_tokens":3}}`,
			text:      "Spoken words",
		},
		{
			name:      "audio translation",
			operation: "(resource) audio.translations > (method) create",
			input:     `{"text":"Translated words"}`,
			text:      "Translated words",
		},
		{
			name:      "empty transcription details",
			operation: "(resource) audio.transcriptions > (method) create",
			input:     `{"text":"Spoken words","segments":[],"words":[],"logprobs":null}`,
			text:      "Spoken words",
		},
		{
			name:  "empty chat log probabilities",
			input: `{"object":"chat.completion","choices":[{"message":{"content":"Hello"},"logprobs":null}]}`,
			text:  "Hello",
		},
		{
			name:  "empty response log probabilities",
			input: `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Hello","logprobs":[]}]}]}`,
			text:  "Hello",
		},
		{
			name:  "empty completion",
			input: `{"object":"text_completion","choices":[{"text":""}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := gjson.Parse(test.input)
			actual := projectText(value, transformers.Route{Operation: test.operation, OutputKind: transformers.OutputResponse})
			require.Equal(t, TextValue{Text: test.text, IsText: true}, actual)
			require.Equal(t, test.input, value.Raw)
		})
	}
}

func TestReadablePreservesRequestedDetailedContent(t *testing.T) {
	for _, test := range []struct {
		name, input string
		route       transformers.Route
	}{
		{
			name:  "transcription word timestamps",
			input: `{"text":"Hello","words":[{"word":"Hello","start":0,"end":0.5}]}`,
			route: transformers.Route{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputResponse},
		},
		{
			name:  "diarized transcription speakers",
			input: `{"text":"Hello","segments":[{"text":"Hello","speaker":"speaker_0","start":0,"end":0.5}]}`,
			route: transformers.Route{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputResponse},
		},
		{
			name:  "translation segments",
			input: `{"text":"Hello","segments":[{"text":"Hello","start":0,"end":0.5}]}`,
			route: transformers.Route{Operation: "(resource) audio.translations > (method) create", OutputKind: transformers.OutputResponse},
		},
		{
			name:  "transcription log probabilities",
			input: `{"text":"Hello","logprobs":[{"token":"Hello","logprob":-0.1}]}`,
			route: transformers.Route{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputResponse},
		},
		{
			name:  "chat log probabilities",
			input: `{"object":"chat.completion","choices":[{"message":{"content":"Hello"},"logprobs":{"content":[{"token":"Hello","logprob":-0.1}]}}]}`,
		},
		{
			name:  "completion log probabilities",
			input: `{"object":"text_completion","choices":[{"text":"Hello","logprobs":{"tokens":["Hello"],"token_logprobs":[-0.1]}}]}`,
		},
		{
			name:  "response text log probabilities",
			input: `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Hello","logprobs":[{"token":"Hello","logprob":-0.1}]}]}]}`,
		},
		{
			name:  "chat delta log probabilities",
			input: `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":{"content":[{"token":"Hello","logprob":-0.1}]}}]}`,
			route: transformers.Route{OutputKind: transformers.OutputStreamEvent},
		},
		{
			name:  "completion delta log probabilities",
			input: `{"object":"text_completion","choices":[{"index":0,"text":"Hello","logprobs":{"tokens":["Hello"],"token_logprobs":[-0.1]}}]}`,
			route: transformers.Route{OutputKind: transformers.OutputStreamEvent},
		},
		{
			name:  "response delta log probabilities",
			input: `{"type":"response.output_text.delta","delta":"Hello","logprobs":[{"token":"Hello","logprob":-0.1}]}`,
			route: transformers.Route{OutputKind: transformers.OutputStreamEvent},
		},
		{
			name:  "response done log probabilities",
			input: `{"type":"response.output_text.done","text":"Hello","logprobs":[{"token":"Hello","logprob":-0.1}]}`,
			route: transformers.Route{OutputKind: transformers.OutputStreamEvent},
		},
		{
			name:  "transcript delta log probabilities",
			input: `{"type":"transcript.text.delta","delta":"Hello","logprobs":[{"token":"Hello","logprob":-0.1}]}`,
			route: transformers.Route{OutputKind: transformers.OutputStreamEvent},
		},
		{
			name:  "transcript done log probabilities",
			input: `{"type":"transcript.text.done","text":"Hello","logprobs":[{"token":"Hello","logprob":-0.1}]}`,
			route: transformers.Route{OutputKind: transformers.OutputStreamEvent},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := gjson.Parse(test.input)
			require.Equal(t, TextValue{}, projectText(value, test.route))
			require.Equal(t, test.input, value.Raw)
		})
	}
}

func TestReadablePageItemsKeepIndependentRecords(t *testing.T) {
	// Identical text belongs to distinct stored results; both IDs must remain.
	for _, id := range []string{"chat_one", "chat_two"} {
		input := `{"id":"` + id + `","object":"chat.completion","choices":[{"message":{"content":"Same answer"}}]}`
		value := gjson.Parse(input)
		require.Equal(t, TextValue{}, projectText(value, transformers.Route{
			Operation: "(resource) chat.completions > (method) list", OutputKind: transformers.OutputPageItem,
		}))
		require.Equal(t, input, value.Raw)
	}
	input := `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}`
	require.Equal(t, TextValue{}, projectText(gjson.Parse(input), transformers.Route{OutputKind: transformers.OutputPageItem}))
}

func TestReadablePreservesMixedUnknownAndUnsuccessfulValues(t *testing.T) {
	tests := map[string]string{
		"unknown text":          `{"text":"a label","data":[1,2]}`,
		"unknown nested text":   `{"output":[{"type":"message","content":[{"type":"output_text","text":"label"}]}]}`,
		"unknown object":        `{"object":"custom","text":"a label"}`,
		"scalar":                `"plain string"`,
		"array":                 `[{"text":"a label"}]`,
		"null":                  `null`,
		"missing response text": `{"object":"response","output":[{"type":"message","content":[{"type":"output_text"}]}]}`,
		"invalid response text": `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":{"nested":"value"}}]}]}`,
		"empty response output": `{"object":"response","output":[]}`,
		"function call":         `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Working"}]},{"type":"function_call","name":"test","arguments":"{}"}]}`,
		"image output":          `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Image"}]},{"type":"image_generation_call","result":"synthetic"}]}`,
		"mixed message content": `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Image"},{"type":"output_image","data":"synthetic"}]}]}`,
		"new message content":   `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Hello","future_content":{"value":1}}]}]}`,
		"text annotations":      `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Source","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}]}`,
		"meaningful reasoning":  `{"object":"response","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Reason"}]},{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}`,
		"encrypted reasoning":   `{"object":"response","output":[{"type":"reasoning","summary":[],"encrypted_content":"synthetic"},{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}`,
		"incomplete response":   `{"object":"response","status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]}]}`,
		"cancelled response":    `{"object":"response","status":"cancelled","output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]}]}`,
		"failed response":       `{"object":"response","status":"failed","error":{"message":"Synthetic failure"},"output":[]}`,
		"incomplete details":    `{"object":"response","status":"completed","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]}]}`,
		"incomplete message":    `{"object":"response","status":"completed","output":[{"type":"message","status":"incomplete","content":[{"type":"output_text","text":"Partial"}]}]}`,
		"in progress response":  `{"object":"response","status":"in_progress","output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]}]}`,
		"tool chat":             `{"object":"chat.completion","choices":[{"message":{"content":"Working","tool_calls":[{"type":"function","function":{"name":"test"}}]}}]}`,
		"legacy tool chat":      `{"object":"chat.completion","choices":[{"message":{"content":"Working","function_call":{"name":"test"}}}]}`,
		"audio chat":            `{"object":"chat.completion","choices":[{"message":{"content":"Hi","audio":{"data":"synthetic"}}}]}`,
		"array chat content":    `{"object":"chat.completion","choices":[{"message":{"content":[{"type":"text","text":"Hi"}]}}]}`,
		"multiple chat choices": `{"object":"chat.completion","choices":[{"message":{"content":"One"}},{"message":{"content":"Two"}}]}`,
		"multiple completions":  `{"object":"text_completion","choices":[{"text":"One"},{"text":"Two"}]}`,
		"length finish":         `{"object":"chat.completion","choices":[{"finish_reason":"length","message":{"content":"Partial"}}]}`,
		"content filter finish": `{"object":"text_completion","choices":[{"finish_reason":"content_filter","text":"Partial"}]}`,
		"future finish reason":  `{"object":"text_completion","choices":[{"finish_reason":"future_status","text":"Partial"}]}`,
		"completion with error": `{"object":"text_completion","error":{"message":"Failure"},"choices":[{"text":"Partial"}]}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			value := gjson.Parse(input)
			require.Equal(t, TextValue{}, projectText(value, transformers.Route{OutputKind: transformers.OutputResponse}))
			require.Equal(t, input, value.Raw)
		})
	}
	// A familiar property name on an unrelated or future operation is insufficient.
	for _, operation := range []string{"", "responses.input_items", "(resource) files > (method) content", "(resource) future > (method) create"} {
		require.Equal(t, TextValue{}, projectText(gjson.Parse(`{"text":"label"}`), transformers.Route{Operation: operation}))
	}
	for _, input := range []string{
		`{"text":"Partial","status":"cancelled"}`,
		`{"text":"Partial","status":"failed"}`,
		`{"text":"Speech","audio":{"data":"synthetic"}}`,
		`{"text":"Speech","future_content":{"value":1}}`,
	} {
		require.Equal(t, TextValue{}, projectText(gjson.Parse(input), transformers.Route{Operation: "(resource) audio.transcriptions > (method) create"}))
	}
}

func TestReadableStreamEvents(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		input     string
		want      TextValue
	}{
		{
			name:  "response delta",
			input: `{"type":"response.output_text.delta","delta":"Hello ","output_index":2,"content_index":1}`,
			want:  TextValue{Text: "Hello ", IsText: true, Delta: true, Key: "response:2:1"},
		},
		{
			name:  "refusal delta",
			input: `{"type":"response.refusal.delta","delta":"Cannot ","output_index":0,"content_index":0}`,
			want:  TextValue{Text: "Cannot ", IsText: true, Delta: true, Key: "response:0:0"},
		},
		{
			name:  "text snapshot",
			input: `{"type":"response.output_text.done","text":"Hello world","output_index":2,"content_index":1}`,
			want:  TextValue{Text: "Hello world", IsText: true, Snapshot: true, Key: "response:2:1"},
		},
		{
			name:  "refusal snapshot",
			input: `{"type":"response.refusal.done","refusal":"Cannot help","output_index":0,"content_index":0}`,
			want:  TextValue{Text: "Cannot help", IsText: true, Snapshot: true, Key: "response:0:0"},
		},
		{
			name:  "completed response",
			input: `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}]}}`,
			want:  TextValue{Text: "Hello", IsText: true, Snapshot: true, Final: true, Parts: []TextPart{{Key: "response:0:0", Text: "Hello"}}},
		},
		{
			name:  "content done snapshot",
			input: `{"type":"response.content_part.done","output_index":2,"content_index":1,"part":{"type":"output_text","text":"Hello"}}`,
			want:  TextValue{Text: "Hello", IsText: true, Snapshot: true, Key: "response:2:1"},
		},
		{
			name:  "message done snapshot",
			input: `{"type":"response.output_item.done","output_index":2,"item":{"type":"message","status":"completed","content":[{"type":"refusal","refusal":"Cannot help"}]}}`,
			want:  TextValue{Text: "Cannot help", IsText: true, Snapshot: true, Key: "response:2:0"},
		},
		{
			name:  "message with multiple completed parts",
			input: `{"type":"response.output_item.done","output_index":2,"item":{"type":"message","status":"completed","content":[{"type":"output_text","text":"One"},{"type":"output_text","text":"Two"}]}}`,
			want: TextValue{Text: "One\n\nTwo", IsText: true, Snapshot: true, Key: "response:2:*", Parts: []TextPart{
				{Key: "response:2:0", Text: "One"}, {Key: "response:2:1", Text: "Two"},
			}},
		},
		{
			name:  "transcription delta",
			input: `{"type":"transcript.text.delta","delta":"Spoken "}`,
			want:  TextValue{Text: "Spoken ", IsText: true, Delta: true, Key: "transcript"},
		},
		{
			name:  "transcription final",
			input: `{"type":"transcript.text.done","text":"Spoken words"}`,
			want:  TextValue{Text: "Spoken words", IsText: true, Snapshot: true, Final: true, Key: "transcript"},
		},
		{
			name:  "chat delta",
			input: `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "},"finish_reason":null}]}`,
			want:  TextValue{Text: "Hello ", IsText: true, Delta: true, Key: "choice:0"},
		},
		{
			name:  "chat refusal delta",
			input: `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":null,"refusal":"Cannot "}}]}`,
			want:  TextValue{Text: "Cannot ", IsText: true, Delta: true, Key: "choice:0"},
		},
		{
			name:      "completion delta",
			operation: "(resource) completions > (method) create",
			input:     `{"object":"text_completion","choices":[{"index":0,"text":"Hello "}]}`,
			want:      TextValue{Text: "Hello ", IsText: true, Delta: true, Key: "choice:0"},
		},
		{
			name:  "empty message scaffold",
			input: `{"type":"response.output_item.added","item":{"type":"message","status":"in_progress","content":[]}}`,
			want:  TextValue{Skip: true},
		},
		{
			name:  "empty content scaffold",
			input: `{"type":"response.content_part.added","part":{"type":"output_text","text":"","annotations":[]}}`,
			want:  TextValue{Skip: true},
		},
		{
			name:  "response created scaffold",
			input: `{"type":"response.created","response":{"status":"in_progress","output":[],"usage":null}}`,
			want:  TextValue{Skip: true},
		},
		{
			name:  "response in progress scaffold",
			input: `{"type":"response.in_progress","response":{"status":"in_progress","output":[]}}`,
			want:  TextValue{Skip: true},
		},
		{
			name:  "chat role scaffold",
			input: `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			want:  TextValue{Skip: true},
		},
		{
			name:  "chat normal termination",
			input: `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			want:  TextValue{Skip: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, projectText(gjson.Parse(test.input), transformers.Route{Operation: test.operation, OutputKind: transformers.OutputStreamEvent}))
		})
	}
}

func TestReadableCompletedPartsKeepOriginalIndexes(t *testing.T) {
	response := `{"status":"completed","output":[{"type":"reasoning","summary":[]},{"type":"message","content":[{"type":"output_text","text":"One"},{"type":"refusal","refusal":"Two"}]},{"type":"reasoning","summary":[]},{"type":"message","content":[{"type":"output_text","text":"Three"}]}]}`
	value := projectText(gjson.Parse(`{"type":"response.completed","response":`+response+`}`), transformers.Route{OutputKind: transformers.OutputStreamEvent})
	require.True(t, value.IsText)
	require.True(t, value.Final)
	require.True(t, value.Snapshot)
	require.Equal(t, "One\n\nTwo\n\nThree", value.Text)
	require.Equal(t, []TextPart{
		{Key: "response:1:0", Text: "One"},
		{Key: "response:1:1", Text: "Two"},
		{Key: "response:3:0", Text: "Three"},
	}, value.Parts)
	// A normal response remains a single text projection without stream state.
	plain := projectText(gjson.Parse(response), transformers.Route{Operation: "(resource) responses > (method) create", OutputKind: transformers.OutputResponse})
	require.Equal(t, TextValue{Text: value.Text, IsText: true}, plain)
}

func TestReadableStreamCompletionRetainsUsageSeparately(t *testing.T) {
	usage := `{"input_tokens":3,"output_tokens":2,"total_tokens":5,"details":{"cached_tokens":1,"future_count":9007199254740993}}`
	response := `{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}],"usage":` + usage + `}`
	for _, input := range []string{
		`{"type":"response.completed","response":` + response + `}`,
		`{"type":"transcript.text.done","text":"Hello","usage":` + usage + `}`,
	} {
		value := projectText(gjson.Parse(input), transformers.Route{OutputKind: transformers.OutputStreamEvent})
		require.True(t, value.IsText)
		require.True(t, value.Snapshot)
		require.Equal(t, "Hello", value.Text)
		require.Equal(t, `{"usage":`+usage+`}`, value.Details.Raw)
		require.Equal(t, usage, value.Details.Get("usage").Raw)
		require.False(t, value.Details.Get("text").Exists())
		require.False(t, value.Details.Get("output").Exists())
	}
	// Ordinary replies still project text; usage is not added to that policy.
	plain := projectText(gjson.Parse(response), transformers.Route{OutputKind: transformers.OutputResponse})
	require.Equal(t, TextValue{Text: "Hello", IsText: true}, plain)
	plain = projectText(gjson.Parse(`{"text":"Hello","usage":`+usage+`}`), transformers.Route{
		Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputResponse,
	})
	require.Equal(t, TextValue{Text: "Hello", IsText: true}, plain)
	for _, emptyUsage := range []string{`null`, `{}`, `[]`} {
		value := projectText(gjson.Parse(`{"type":"transcript.text.done","text":"Hello","usage":`+emptyUsage+`}`), transformers.Route{OutputKind: transformers.OutputStreamEvent})
		require.Equal(t, gjson.Result{}, value.Details)
	}
}

func TestReadableStreamPreservesNontextAndFailureEvents(t *testing.T) {
	tests := map[string]string{
		"unknown event":             `{"type":"future.text.delta","delta":"Hello"}`,
		"unknown text event":        `{"type":"future.event","text":"Hello","data":123}`,
		"malformed text delta":      `{"type":"response.output_text.delta","delta":{"text":"Hello"}}`,
		"missing text delta":        `{"type":"response.output_text.delta"}`,
		"tool and text delta":       `{"type":"response.output_text.delta","delta":"Hello","tool_calls":[{"name":"test"}]}`,
		"mixed delta item":          `{"type":"response.output_text.delta","delta":"Hello","item":{"type":"function_call","name":"test"}}`,
		"function arguments delta":  `{"type":"response.function_call_arguments.delta","delta":"{}"}`,
		"image delta":               `{"type":"response.image_generation_call.partial_image","partial_image_b64":"synthetic"}`,
		"incomplete event":          `{"type":"response.incomplete","response":{"status":"incomplete","output":[]}}`,
		"failed event":              `{"type":"response.failed","response":{"status":"failed","error":{"message":"Synthetic failure"}}}`,
		"cancelled event":           `{"type":"response.cancelled","response":{"status":"cancelled"}}`,
		"error event":               `{"type":"error","message":"Synthetic failure"}`,
		"incomplete final snapshot": `{"type":"response.completed","response":{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]}]}}`,
		"mixed final snapshot":      `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Partial"}]},{"type":"function_call","name":"test"}]}}`,
		"mixed completed item":      `{"type":"response.output_item.done","item":{"type":"message","status":"completed","content":[{"type":"output_text","text":"Image"},{"type":"output_image","data":"synthetic"}]}}`,
		"failed scaffold":           `{"type":"response.in_progress","response":{"status":"failed","output":[]}}`,
		"scaffold containing usage": `{"type":"response.created","response":{"status":"in_progress","output":[],"usage":{"total_tokens":12}}}`,
		"scaffold containing tools": `{"type":"response.output_item.added","item":{"type":"function_call","name":"test","arguments":""}}`,
		"incomplete item done":      `{"type":"response.output_item.done","item":{"type":"message","status":"incomplete","content":[{"type":"output_text","text":"Partial"}]}}`,
		"tool chat delta":           `{"object":"chat.completion.chunk","choices":[{"delta":{"content":"Working","tool_calls":[{"index":0,"function":{"name":"test"}}]}}]}`,
		"multiple chat deltas":      `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"One"}},{"index":1,"delta":{"content":"Two"}}]}`,
		"chat incomplete end":       `{"object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"length"}]}`,
		"chat usage only":           `{"object":"chat.completion.chunk","choices":[],"usage":{"total_tokens":12}}`,
		"chat usage with scaffold":  `{"object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"total_tokens":12}}`,
		"diarized segment":          `{"type":"transcript.text.segment","text":"Hello","speaker":"A","start":0,"end":1}`,
		"assistant message delta":   `{"event":"thread.message.delta","data":{"id":"msg_fake","object":"thread.message.delta","delta":{"content":[{"index":0,"type":"text","text":{"value":"Hello","annotations":[]}}]}}}`,
		"assistant run action":      `{"event":"thread.run.requires_action","data":{"id":"run_fake","object":"thread.run","status":"requires_action","required_action":{"type":"submit_tool_outputs","submit_tool_outputs":{"tool_calls":[{"id":"call_fake","type":"function","function":{"name":"test","arguments":"{}"}}]}}}}`,
		"assistant failed run":      `{"event":"thread.run.failed","data":{"id":"run_fake","object":"thread.run","status":"failed","last_error":{"code":"synthetic","message":"Synthetic failure"}}}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, TextValue{}, projectText(gjson.Parse(input), transformers.Route{OutputKind: transformers.OutputStreamEvent}))
		})
	}
}

func TestReadableTextIsUnmodifiedAndUnbounded(t *testing.T) {
	text := "\x1b[31m\n" + strings.Repeat("large synthetic text Ω\n", 100000) + "\x00"
	encoded, err := json.Marshal(text)
	require.NoError(t, err)
	input := `{"object":"chat.completion","choices":[{"message":{"content":` + string(encoded) + `}}]}`
	require.Equal(t, TextValue{Text: text, IsText: true}, projectText(gjson.Parse(input), transformers.Route{OutputKind: transformers.OutputResponse}))
	stream := `{"type":"response.output_text.delta","delta":` + string(encoded) + `,"output_index":0,"content_index":0}`
	require.Equal(t, TextValue{Text: text, IsText: true, Delta: true, Key: "response:0:0"}, projectText(gjson.Parse(stream), transformers.Route{OutputKind: transformers.OutputStreamEvent}))
}
