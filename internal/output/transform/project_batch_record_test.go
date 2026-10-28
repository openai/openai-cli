package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func batchProjectionResult(body string) string {
	return `{"id":"batch_req_demo","custom_id":"request-1","response":{"status_code":200,"request_id":"req_demo","body":` + body + `},"error":null}`
}

func TestProjectBatchRecordOrdinaryResponse(t *testing.T) {
	body := `{"object":"response","status":"completed","model":"synthetic-model","output":[{"type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"positive","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":31,"output_tokens":5,"total_tokens":36,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}},"access_programs":null,"billing":{"payer":"developer"},"moderation":null,"tool_usage":{"image_gen":{"input_tokens":0,"input_tokens_details":{"image_tokens":0,"text_tokens":0},"output_tokens":0,"output_tokens_details":{"image_tokens":0,"text_tokens":0},"total_tokens":0},"web_search":{"num_requests":0}},"reasoning":{"context":"current_turn","effort":"none","mode":"standard","summary":null},"temperature":1,"store":false}`
	result, err := ProjectBatchRecord(context.Background(), gjson.Parse(batchProjectionResult(body)), "result")
	require.NoError(t, err)
	require.Equal(t, "positive", result.Get("output").Str)
	require.Equal(t, "completed", result.Get("outcome").Str)
	require.Equal(t, "developer", result.Get("billing.payer").Str)
	require.Equal(t, "31", result.Get("usage.input_tokens").Raw)
	for _, key := range []string{"temperature", "store", "reasoning", "tool_usage", "usage.input_tokens_details", "usage.output_tokens_details"} {
		require.False(t, result.Get(key).Exists(), key)
	}
}

func TestProjectBatchRecordPreservesOrderedOutputAndMeaningfulDetails(t *testing.T) {
	body := `{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"first"},{"type":"refusal","refusal":"cannot comply"}]},{"type":"function_call","name":"lookup","arguments":"{\"n\":1}"},{"type":"message","status":"incomplete","content":[{"type":"output_text","text":"partial"}]},{"type":"future","value":9007199254740993}],"usage":{"input_tokens":0,"output_tokens":0,"input_tokens_details":{"cached_tokens":3,"future":0}},"billing":{"payer":"new_payer","unknown":null},"moderation":{"flagged":false},"tool_usage":{"web_search":{"num_requests":1},"future":{"used":0}}}`
	result, err := ProjectBatchRecord(context.Background(), gjson.Parse(batchProjectionResult(body)), "result")
	require.NoError(t, err)
	require.Equal(t, "first", result.Get("output.0").Str)
	require.Equal(t, "cannot comply", result.Get("output.1.refusal").Str)
	require.Equal(t, "lookup", result.Get("output.2.name").Str)
	require.Equal(t, "partial", result.Get("output.3.content.0.text").Str)
	require.Equal(t, "9007199254740993", result.Get("output.4.value").Raw)
	require.Contains(t, result.Get("content_outcome").Str, "refusal")
	require.Contains(t, result.Get("output_state").Str, "not completed")
	for key, want := range map[string]string{"usage.input_tokens": "0", "usage.input_tokens_details.future": "0", "billing.unknown": "null", "moderation.flagged": "false", "tool_usage.web_search.num_requests": "1", "tool_usage.future.used": "0"} {
		require.Equal(t, want, result.Get(key).Raw, key)
	}
}

func TestProjectBatchRecordInputPreservesContext(t *testing.T) {
	value := gjson.Parse(`{"custom_id":"request-1","method":"POST","url":"/v1/responses","body":{"model":"synthetic-model","instructions":"Follow these instructions","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image"},{"type":"input_text","text":"describe this"}]}],"previous_response_id":"resp_previous","conversation":{"id":"conv_demo"},"tools":[{"type":"function","name":"lookup"}],"max_output_tokens":32}}`)
	result, err := ProjectBatchRecord(context.Background(), value, "matched_input")
	require.NoError(t, err)
	for key, want := range map[string]string{"instructions": "Follow these instructions", "previous_response_id": "resp_previous", "conversation.id": "conv_demo", "input.0.content.1.text": "describe this", "tools.0.name": "lookup"} {
		require.Equal(t, want, result.Get(key).String(), key)
	}
	require.False(t, result.Get("max_output_tokens").Exists())
}

func TestProjectBatchRecordFallsBackWithoutLosingMalformedIdentityOrUnknownFields(t *testing.T) {
	ordinary := batchProjectionResult(`{"object":"response","status":"completed","output":[]}`)
	for _, value := range []string{
		strings.Replace(ordinary, `"custom_id":"request-1"`, `"custom_id":9007199254740993`, 1),
		strings.Replace(ordinary, `"custom_id":"request-1"`, `"custom_id":null`, 1),
		strings.Replace(ordinary, `"custom_id":"request-1"`, `"custom_id":""`, 1),
		strings.Replace(ordinary, `"output":[]`, `"output":[],"future":null`, 1),
		strings.Replace(ordinary, `"status_code":200`, `"status_code":200.5`, 1),
		strings.Replace(ordinary, `"status_code":200`, `"status_code":200,"status_code":400`, 1),
	} {
		for _, kind := range []string{"result", "matched_result"} {
			result, err := ProjectBatchRecord(context.Background(), gjson.Parse(value), kind)
			require.NoError(t, err)
			require.False(t, result.Exists(), "%s: %s", kind, value)
		}
	}
}

func TestProjectBatchRecordChatOutcomesRemainDistinct(t *testing.T) {
	for _, reason := range []string{"stop", "length", "content_filter", "tool_calls", "future"} {
		body := fmt.Sprintf(`{"object":"chat.completion","choices":[{"index":7,"message":{"role":"assistant","content":"answer","refusal":"refused","tool_calls":[{"id":"call_demo","function":{"name":"lookup"}}]},"finish_reason":%q}]}`, reason)
		result, err := ProjectBatchRecord(context.Background(), gjson.Parse(batchProjectionResult(body)), "result")
		require.NoError(t, err)
		require.Equal(t, "7", result.Get("choice").Raw)
		require.Equal(t, "answer", result.Get("output").Str)
		require.Equal(t, "refused", result.Get("refusal").Str)
		require.Equal(t, "call_demo", result.Get("tool_calls.0.id").Str)
		if reason != "stop" {
			require.Equal(t, reason, result.Get("finish_reason").Str)
			require.NotEqual(t, "completed", result.Get("outcome").Str)
		}
	}
}

func TestProjectBatchRecordHTTP200DoesNotEstablishCompletion(t *testing.T) {
	for _, body := range []string{`{"object":"chat.completion","choices":[]}`, `{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":null},"finish_reason":null}]}`, `{"object":"response","status":"completed","output":[]}`} {
		result, err := ProjectBatchRecord(context.Background(), gjson.Parse(batchProjectionResult(body)), "result")
		require.NoError(t, err)
		require.True(t, result.Exists())
		if result.Get("outcome").Str == "completed" {
			require.Equal(t, "no output returned", result.Get("content_outcome").Str)
		} else {
			require.Contains(t, result.Get("outcome").Str, "completion not established")
		}
	}
}

func TestProjectBatchRecordEmptyChatRefusalIsNotARefusal(t *testing.T) {
	for _, refusal := range []string{`null`, `""`, `"Cannot comply."`, `false`, `0`, `{}`} {
		body := `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"normal answer","refusal":` + refusal + `},"finish_reason":"stop"}]}`
		result, err := ProjectBatchRecord(context.Background(), gjson.Parse(batchProjectionResult(body)), "result")
		require.NoError(t, err)
		switch refusal {
		case `null`, `""`:
			require.Equal(t, "normal answer", result.Get("output").Str)
			require.False(t, result.Get("content_outcome").Exists())
			require.False(t, result.Get("refusal").Exists())
		case `"Cannot comply."`:
			require.Equal(t, "refused", result.Get("content_outcome").Str)
			require.Equal(t, "Cannot comply.", result.Get("refusal").Str)
		default:
			require.False(t, result.Exists(), "Unexpected refusal type requires complete rendering: %s", refusal)
		}
	}
}

func TestProjectBatchRecordCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ProjectBatchRecord(ctx, gjson.Parse(`{}`), "result")
	require.ErrorIs(t, err, context.Canceled)
}

func TestProjectBatchRecordPreservesExtensionFieldNames(t *testing.T) {
	for _, key := range []string{"future\x1b[31m", "future\x00\a\x1f\x7f", "future\n\t\r", "future\\\"", "future\u0085\u2028\u2029😀"} {
		for _, object := range []string{"response", "chat.completion"} {
			t.Run(fmt.Sprintf("%s/%q", object, key), func(t *testing.T) {
				body := map[string]any{
					"object": object,
					"usage":  map[string]any{key: json.Number("9007199254740993"), "input_tokens_details": map[string]any{key: 7}},
				}
				if object == "response" {
					body["status"], body["output"] = "completed", []any{}
					body["tool_usage"] = map[string]any{key: map[string]any{"num_requests": 9}}
				} else {
					body["choices"] = []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "answer"}, "finish_reason": "stop"}}
				}
				encoded, err := json.Marshal(body)
				require.NoError(t, err)
				for _, kind := range []string{"result", "matched_result"} {
					result, err := ProjectBatchRecord(t.Context(), gjson.Parse(batchProjectionResult(string(encoded))), kind)
					require.NoError(t, err)
					var document map[string]json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(result.Raw), &document), kind)
					var usage map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(document["usage"], &usage))
					require.Equal(t, "9007199254740993", string(usage[key]))
					var details map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(usage["input_tokens_details"], &details))
					require.Equal(t, "7", string(details[key]))
					if object == "response" {
						var tools map[string]map[string]int
						require.NoError(t, json.Unmarshal(document["tool_usage"], &tools))
						require.Equal(t, 9, tools[key]["num_requests"])
					}
				}
			})
		}
	}
}
