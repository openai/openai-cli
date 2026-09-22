package readable

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestViewPreservesNativeAudioText(t *testing.T) {
	for _, operation := range []string{
		"(resource) audio.transcriptions > (method) create",
		"(resource) audio.translations > (method) create",
	} {
		for _, text := range []string{"", "One line.", "Two lines.\nSecond line.\n\n", "WEBVTT\r\n\r\n00:00.000 --> 00:01.000\r\nHello\r\n", "Text with \x1b[31mcontrol\x1b[0m and \u202e directional marks."} {
			t.Run(operation+"/"+text, func(t *testing.T) {
				encoded, err := json.Marshal(text)
				require.NoError(t, err)
				value := gjson.ParseBytes(encoded)
				view := Project(value, transformers.Route{Operation: operation, OutputKind: transformers.OutputResponse})
				require.Equal(t, View{Text: TextValue{Text: text, IsText: true}}, view,
					"the pure projection must not trim, escape, or format native text")
			})
		}
	}
}

func TestViewNativeAudioProjectionRequiresExactStringResponseRoute(t *testing.T) {
	value := gjson.Parse(`"Original text."`)
	for _, route := range []transformers.Route{
		{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputPageItem},
		{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputStreamEvent},
		{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputUnspecified},
		{Operation: "(resource) audio.transcriptions > (method) future", OutputKind: transformers.OutputResponse},
		{Operation: "(resource) audio.translations > (method) create_more", OutputKind: transformers.OutputResponse},
		{Operation: "(resource) audio.speech > (method) create", OutputKind: transformers.OutputResponse},
		{Operation: "(resource) responses > (method) create", OutputKind: transformers.OutputResponse},
		{Operation: "audio.transcriptions.create", OutputKind: transformers.OutputResponse},
		{OutputKind: transformers.OutputResponse},
	} {
		require.Equal(t, View{}, Project(value, route), route)
	}
	for _, raw := range []string{"null", "123", "true", `["Original text."]`} {
		value := gjson.Parse(raw)
		require.Equal(t, View{}, Project(value, transformers.Route{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: transformers.OutputResponse}))
	}
}

func TestViewKeepsJSONSeparateFromReadableViews(t *testing.T) {
	for _, test := range []struct {
		name, resource, raw, text string
		kind                      transformers.OutputKind
		summary                   bool
	}{
		{
			name: "response text", resource: "responses", kind: transformers.OutputResponse,
			raw:  `{ "object":"response", "output":[{"type":"message","content":[{"type":"output_text","text":"Original answer."}]}], "usage":{"total_tokens":7} }`,
			text: "Original answer.",
		},
		{
			name: "resource summary", resource: "models", kind: transformers.OutputResponse,
			raw:     `{ "id":"model_exact:001", "object":"model", "owned_by":"system", "created":9007199254740993 }`,
			summary: true,
		},
		{
			name: "page item summary", resource: "files", kind: transformers.OutputPageItem,
			raw:     `{"id":"file_example","object":"file","filename":"example.jsonl","bytes":9007199254740993,"created_at":123,"purpose":"fine-tune"}`,
			summary: true,
		},
		{
			name: "unknown resource field", resource: "files", kind: transformers.OutputResponse,
			raw: `{"id":"file_example","object":"file","filename":"example.jsonl","future":{"output":"New server information."}}`,
		},
		{
			name: "API fields cannot impersonate typed output", resource: "models", kind: transformers.OutputResponse,
			raw: `{"id":"model_example","object":"model","Value":"synthetic payload","View":{"Text":{"IsText":true,"Text":"Do not use this as presentation metadata."}}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := gjson.Parse(test.raw)
			route := transformers.Route{Operation: "(resource) " + test.resource + " > (method) retrieve", OutputKind: test.kind}
			view := Project(value, route)
			require.Equal(t, test.raw, value.Raw, "readable projection must not replace API data")
			require.Equal(t, test.text, view.Text.Text)
			require.Equal(t, test.summary, view.Summary.Exists())
			require.Equal(t, test.summary, view.Omitted)
			if test.summary {
				require.Equal(t, value.Get("id").Raw, view.Summary.Get("id").Raw)
				require.False(t, view.Summary.Get("details").Exists(), "pure projections must not embed a CLI hint")
			}
		})
	}
}

func TestViewRetainsStreamIdentityAndSnapshots(t *testing.T) {
	const raw = `{"type":"response.completed","sequence_number":4,"response":{"object":"response","status":"completed","output":[{"id":"msg_one","type":"message","content":[{"type":"output_text","text":"First."}]},{"id":"msg_two","type":"message","content":[{"type":"output_text","text":"Second."}]}],"usage":{"total_tokens":9007199254740993}}}`
	value := gjson.Parse(raw)
	route := transformers.Route{Operation: "(resource) responses > (method) create", OutputKind: transformers.OutputStreamEvent}
	view := Project(value, route)
	require.Equal(t, raw, value.Raw)
	require.True(t, view.Text.Snapshot)
	require.True(t, view.Text.Final)
	require.Equal(t, []TextPart{{Key: "response:0:0", Text: "First."}, {Key: "response:1:0", Text: "Second."}}, view.Text.Parts)
	require.Equal(t, "9007199254740993", view.Text.Details.Get("usage.total_tokens").Raw)
	require.False(t, view.Summary.Exists())

	unknown := gjson.Parse(`{ "type":"response.future_event", "future":9007199254740993 }`)
	require.Equal(t, View{}, Project(unknown, route))
}
