package transformers

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStreamFailure(t *testing.T) {
	for _, kind := range []string{"response.failed", "response.incomplete", "response.cancelled", "thread.run.failed", "thread.run.incomplete", "thread.run.expired", "thread.run.cancelled", "thread.run.step.failed", "thread.run.step.cancelled", "thread.run.step.expired", "thread.message.incomplete", "error"} {
		t.Run(kind, func(t *testing.T) {
			require.NotEmpty(t, StreamFailure(gjson.Parse(`{"type":"`+kind+`"}`)))
			require.NotEmpty(t, StreamFailure(gjson.Parse(`{"event":"`+kind+`","data":{"message":"private synthetic message"}}`)))
			require.NotContains(t, StreamFailure(gjson.Parse(`{"type":"`+kind+`","message":"private synthetic message"}`)), "private")
		})
	}
	for _, value := range []string{
		`{"type":"response.done","response":{"status":"failed"}}`,
		`{"type":"response.completed","response":{"status":"incomplete"}}`,
		`{"type":"response.failed","event":"future_metadata","response":{"status":"failed"}}`,
		`{"type":"response.failed","event":"thread.run.completed"}`,
	} {
		require.NotEmpty(t, StreamFailure(gjson.Parse(value)))
	}
	for _, value := range []string{
		`{"event":"thread.run.completed","data":{"status":"completed"}}`,
		`{"event":"thread.run.requires_action","data":{"status":"requires_action"}}`,
		`{"type":"response.output_text.delta","delta":"error: response.failed"}`,
		`{"type":"custom.failed","status":"failed","error":"user content"}`,
		`{"type":"custom.metadata","event":"error","data":"user content"}`,
		`{"type":"response.output_text.delta","event":"error","delta":"ordinary text"}`,
		`{"status":"failed"}`, `"response.failed"`, `null`,
	} {
		require.Empty(t, StreamFailure(gjson.Parse(value)))
	}
}
