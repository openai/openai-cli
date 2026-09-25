package transformers

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStreamFailureUsesKnownTextStreamEvents(t *testing.T) {
	for _, kind := range []string{"response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error"} {
		for _, resource := range []string{"responses", "beta.responses"} {
			value := gjson.Parse(`{"type":"` + kind + `","message":"private synthetic details","event":"future"}`)
			message := StreamFailure(value, streamTestRoute(resource))
			require.NotEmpty(t, message, kind)
			require.NotContains(t, message, "private")
		}
	}
	for _, kind := range []string{"response.completed", "response.done"} {
		for _, status := range []string{"failed", "incomplete", "cancelled", "canceled"} {
			require.NotEmpty(t, StreamFailure(gjson.Parse(`{"type":"`+kind+`","response":{"status":"`+status+`"}}`), streamTestRoute("responses")))
		}
	}
	for _, resource := range []string{"chat.completions", "completions"} {
		require.NotEmpty(t, StreamFailure(gjson.Parse(`{"type":"error","error":{"message":"private synthetic details"}}`), streamTestRoute(resource)))
		require.Empty(t, StreamFailure(gjson.Parse(`{"type":"response.failed"}`), streamTestRoute(resource)))
	}
}

func TestStreamFailureDoesNotInferFailureFromUserOrUnknownData(t *testing.T) {
	for _, input := range []string{
		`{"type":"response.output_text.delta","delta":"response.failed","event":"error"}`,
		`{"type":"future.failed","error":"synthetic","status":"failed"}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
		`{"type":"response.in_progress","response":{"status":"in_progress"}}`,
		`{"event":"error","data":"future envelope"}`,
		`{"error":{"message":"synthetic"}}`,
		`{"status":"failed"}`, `"response.failed"`, `null`,
	} {
		require.Empty(t, StreamFailure(gjson.Parse(input), streamTestRoute("responses")), input)
	}
	for _, route := range []Route{
		{"(resource) responses > (method) create", OutputResponse},
		{"(resource) responses > (method) list", OutputStreamEvent},
		{"(resource) images > (method) generate", OutputStreamEvent},
		{"(resource) beta.threads.runs > (method) create", OutputStreamEvent},
		{OutputKind: OutputStreamEvent},
	} {
		require.Empty(t, StreamFailure(gjson.Parse(`{"type":"error"}`), route))
	}
}
