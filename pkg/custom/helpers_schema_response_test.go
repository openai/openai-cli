package custom

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
)

func TestSchemaHelperResponsePreservesCompletedTextBytes(t *testing.T) {
	response := decodeSchemaHelperResponse(t, `{
		"status":"completed",
		"output":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"synthetic reasoning"}]},
			{"type":"message","status":"completed","role":"assistant","content":[
				{"type":"output_text","text":"  {\n\"type\":"},
				{"type":"output_text","text":"\"object\",\"description\":\"雪\"}\n"}
			]}
		]
	}`)
	data, err := schemaHelperResponse(response)
	require.NoError(t, err)
	require.Equal(t, "  {\n\"type\":\"object\",\"description\":\"雪\"}\n", string(data))
	require.NoError(t, validateHelperSchema(t.Context(), data))
}

func TestSchemaHelperResponseRejectsNoncompletedResponses(t *testing.T) {
	data, err := schemaHelperResponse(nil)
	require.Error(t, err)
	require.Nil(t, data)
	for _, status := range []string{"", "queued", "in_progress", "incomplete", "failed", "cancelled", "unexpected"} {
		t.Run(status, func(t *testing.T) {
			response := decodeSchemaHelperResponse(t, `{
				"status":`+jsonStringForSchemaTest(t, status)+`,
				"incomplete_details":{"reason":"max_output_tokens"},
				"output":[{"type":"message","status":"completed","role":"assistant","content":[
					{"type":"output_text","text":"{\"type\":\"object\"}"}
				]}]
			}`)
			data, err := schemaHelperResponse(response)
			require.Error(t, err)
			require.Nil(t, data)
			require.Contains(t, err.Error(), "did not complete")
		})
	}
}

func TestSchemaHelperResponseRejectsUnusableOutputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		cause  string
	}{
		{"missing output", `null`, "no schema"},
		{"empty output", `[]`, "no schema"},
		{"reasoning only", `[{"type":"reasoning","summary":[]}]`, "no schema"},
		{"missing item type", `[{}]`, "unsupported output type"},
		{"tool call", `[{"type":"function_call","name":"synthetic-tool","arguments":"{}"}]`, "unsupported output type"},
		{"unknown output", `[{"type":"future_output"}]`, "unsupported output type"},
		{"incomplete message", `[{"type":"message","status":"incomplete","role":"assistant","content":[{"type":"output_text","text":"{}"}]}]`, "incomplete message"},
		{"in-progress message", `[{"type":"message","status":"in_progress","role":"assistant","content":[{"type":"output_text","text":"{}"}]}]`, "incomplete message"},
		{"missing message status", `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{}"}]}]`, "incomplete message"},
		{"wrong role", `[{"type":"message","status":"completed","role":"user","content":[{"type":"output_text","text":"{}"}]}]`, "incomplete message"},
		{"missing role", `[{"type":"message","status":"completed","content":[{"type":"output_text","text":"{}"}]}]`, "incomplete message"},
		{"empty content", `[{"type":"message","status":"completed","role":"assistant","content":[]}]`, "no schema"},
		{"empty text", `[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":""}]}]`, "no schema"},
		{"whitespace text", `[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":" \t\r\n"}]}]`, "no schema"},
		{"missing content type", `[{"type":"message","status":"completed","role":"assistant","content":[{"text":"{}"}]}]`, "unsupported content"},
		{"unknown content", `[{"type":"message","status":"completed","role":"assistant","content":[{"type":"future_content","text":"{}"}]}]`, "unsupported content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := decodeSchemaHelperResponse(t, `{"status":"completed","output":`+tc.output+`}`)
			data, err := schemaHelperResponse(response)
			require.Error(t, err)
			require.Nil(t, data)
			require.Contains(t, err.Error(), tc.cause)
			require.Contains(t, err.Error(), "No file was saved")
		})
	}
}

func TestSchemaHelperResponseRefusalDiscardsAllText(t *testing.T) {
	const private = "synthetic-private-refusal\x1b[31m"
	for _, output := range []string{
		`[{"type":"message","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":` + jsonStringForSchemaTest(t, private) + `}]}]`,
		`[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"{}"},{"type":"refusal","refusal":` + jsonStringForSchemaTest(t, private) + `}]}]`,
		`[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"{}"}]},{"type":"message","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":` + jsonStringForSchemaTest(t, private) + `}]}]`,
	} {
		response := decodeSchemaHelperResponse(t, `{"status":"completed","output":`+output+`}`)
		data, err := schemaHelperResponse(response)
		require.Error(t, err)
		require.Nil(t, data)
		require.Contains(t, err.Error(), "refused")
		require.NotContains(t, err.Error(), private)
		require.NotContains(t, err.Error(), "\x1b")
	}
}

func TestSchemaHelperResponseMalformedArtifactFailsCompilation(t *testing.T) {
	for _, artifact := range []string{`{"type":`, "```json\n{}\n```", `{} {}`, `null`, `[]`, `{"type":"unsupported"}`} {
		response := decodeSchemaHelperResponse(t, `{"status":"completed","output":[{
			"type":"message","status":"completed","role":"assistant","content":[
				{"type":"output_text","text":`+jsonStringForSchemaTest(t, artifact)+`}
			]}]}`)
		data, err := schemaHelperResponse(response)
		require.NoError(t, err)
		require.Equal(t, artifact, string(data))
		require.Error(t, validateHelperSchema(t.Context(), data))
	}
}

func TestSchemaHelperResponseDoesNotCapArtifactBytes(t *testing.T) {
	artifact := `{"description":"` + strings.Repeat("x", 2*1024*1024) + `"}`
	response := decodeSchemaHelperResponse(t, `{"status":"completed","output":[{
		"type":"message","status":"completed","role":"assistant","content":[
			{"type":"output_text","text":`+jsonStringForSchemaTest(t, artifact)+`}
		]}]}`)
	data, err := schemaHelperResponse(response)
	require.NoError(t, err)
	require.Equal(t, artifact, string(data))
}

func decodeSchemaHelperResponse(t *testing.T, wire string) *responses.Response {
	t.Helper()
	var response responses.Response
	require.NoError(t, json.Unmarshal([]byte(wire), &response))
	return &response
}

func jsonStringForSchemaTest(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
