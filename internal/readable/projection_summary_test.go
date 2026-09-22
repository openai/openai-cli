package readable

import (
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSummaryManagementResources(t *testing.T) {
	for _, test := range []struct {
		resource, input, want string
	}{
		{
			"models",
			`{"id":"gpt-example:exact.id-001","object":"model","created":1234567890,"owned_by":"system","shutdown_date":null}`,
			`{"id":"gpt-example:exact.id-001","owned_by":"system"}`,
		},
		{
			"files",
			`{"object":"file","id":"file_example","bytes":9007199254740993,"created_at":123,"filename":"training data.jsonl","purpose":"fine-tune","status":"processed","expires_at":null,"status_details":null}`,
			`{"id":"file_example","filename":"training data.jsonl","purpose":"fine-tune","bytes":9007199254740993,"status":"processed"}`,
		},
		{
			"batches",
			`{"id":"batch_example","object":"batch","status":"completed","input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_errors","errors":null,"created_at":123,"completion_window":"24h","endpoint":"/v1/responses","metadata":{"label":"example"},"request_counts":{"total":3,"completed":2,"failed":1}}`,
			`{"id":"batch_example","status":"completed","request_counts":{"total":3,"completed":2,"failed":1},"input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_errors","endpoint":"/v1/responses"}`,
		},
		{
			"vector_stores",
			`{"id":"vs_example","object":"vector_store","created_at":123,"file_counts":{"completed":2,"failed":0,"total":2},"last_active_at":124,"metadata":{},"name":"Guide","status":"completed","usage_bytes":0,"expires_after":{"anchor":"last_active_at","days":7},"expires_at":999}`,
			`{"id":"vs_example","name":"Guide","status":"completed","file_counts":{"completed":2,"failed":0,"total":2},"usage_bytes":0,"expires_at":999}`,
		},
		{
			"vector_stores.files",
			`{"id":"file_example","object":"vector_store.file","created_at":123,"status":"completed","last_error":null,"usage_bytes":10,"vector_store_id":"vs_example","attributes":{"section":"appendix"},"chunking_strategy":{"type":"auto"}}`,
			`{"id":"file_example","vector_store_id":"vs_example","status":"completed","usage_bytes":10,"attributes":{"section":"appendix"}}`,
		},
		{
			"vector_stores.file_batches",
			`{"id":"vsfb_example","object":"vector_store.files_batch","created_at":123,"status":"in_progress","vector_store_id":"vs_example","file_counts":{"total":2,"in_progress":1,"completed":1,"failed":0,"cancelled":0}}`,
			`{"id":"vsfb_example","vector_store_id":"vs_example","status":"in_progress","file_counts":{"total":2,"in_progress":1,"completed":1,"failed":0,"cancelled":0}}`,
		},
		{
			"fine_tuning.jobs",
			`{"id":"ftjob_example","object":"fine_tuning.job","created_at":123,"status":"succeeded","model":"gpt-example","fine_tuned_model":"ft:gpt-example:org:exact-id","training_file":"file_training","validation_file":null,"result_files":["file_result"],"trained_tokens":9007199254740993,"error":null,"hyperparameters":{"n_epochs":1},"seed":42,"method":{"type":"supervised"},"metadata":{}}`,
			`{"id":"ftjob_example","status":"succeeded","model":"gpt-example","fine_tuned_model":"ft:gpt-example:org:exact-id","training_file":"file_training","result_files":["file_result"],"trained_tokens":9007199254740993}`,
		},
		{
			"beta.assistants",
			`{"id":"asst_example","object":"assistant","created_at":123,"name":"Help","model":"gpt-example","description":"Answers\nwith examples.","instructions":"Use the reference files.","tools":[{"type":"file_search"}],"metadata":{},"response_format":"auto","temperature":1,"top_p":1,"tool_resources":{}}`,
			`{"id":"asst_example","name":"Help","model":"gpt-example","description":"Answers\nwith examples."}`,
		},
		{
			"beta.threads.runs",
			`{"id":"run_example","object":"thread.run","thread_id":"thread_example","assistant_id":"asst_example","status":"completed","model":"gpt-example","instructions":"Use examples.","tools":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30},"required_action":null,"last_error":null,"incomplete_details":null,"created_at":123,"metadata":{}}`,
			`{"id":"run_example","status":"completed","thread_id":"thread_example","assistant_id":"asst_example","model":"gpt-example","usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
		},
	} {
		t.Run(test.resource, func(t *testing.T) {
			for _, kind := range []transformers.OutputKind{transformers.OutputResponse, transformers.OutputPageItem} {
				input := gjson.Parse(test.input)
				got, ok, _ := summarize(input, transformers.Route{Operation: "(resource) " + test.resource + " > (method) retrieve", OutputKind: kind})
				require.True(t, ok)
				require.Equal(t, test.want, got.Raw)
				require.Equal(t, test.input, input.Raw, "original API value must stay unchanged")
			}
		})
	}
}

func TestSummaryPreservesIssuesAndActionableContent(t *testing.T) {
	for _, test := range []struct {
		resource, input string
		kept            []string
	}{
		{"files", `{"id":"file_example","object":"file","filename":"input.txt","status":"error","status_details":"The file could not be processed."}`, []string{"status", "status_details"}},
		{"models", `{"id":"gpt-example","object":"model","owned_by":"system","shutdown_date":"2026-07-01"}`, []string{"shutdown_date"}},
		{"batches", `{"id":"batch_example","object":"batch","status":"failed","errors":{"data":[{"code":"invalid_request","line":9007199254740993,"param":"model","message":"Unsupported model."}]}}`, []string{"status", "errors"}},
		{"vector_stores.files", `{"id":"file_example","object":"vector_store.file","status":"failed","last_error":{"code":"unsupported_file","message":"Use a supported file."}}`, []string{"status", "last_error"}},
		{"fine_tuning.jobs", `{"id":"ftjob_example","object":"fine_tuning.job","status":"failed","error":{"code":"invalid_file","message":"Review input.","param":"training_file"}}`, []string{"status", "error"}},
		{"beta.threads.runs", `{"id":"run_example","object":"thread.run","status":"requires_action","required_action":{"type":"submit_tool_outputs","submit_tool_outputs":{"tool_calls":[{"id":"call_example","type":"function","function":{"name":"lookup","arguments":"{\"key\":\"example\"}"}}]}}}`, []string{"status", "required_action"}},
		{"beta.threads.runs", `{"id":"run_example","object":"thread.run","status":"incomplete","incomplete_details":{"reason":"max_completion_tokens"},"last_error":{"code":"server_error","message":"Retry later."}}`, []string{"status", "incomplete_details", "last_error"}},
	} {
		t.Run(test.resource+"/"+gjson.Get(test.input, "status").String(), func(t *testing.T) {
			value := gjson.Parse(test.input)
			got, ok, _ := summarize(value, transformers.Route{Operation: "(resource) " + test.resource + " > (method) retrieve", OutputKind: transformers.OutputResponse})
			require.True(t, ok)
			for _, field := range test.kept {
				require.Equal(t, value.Get(field).Raw, got.Get(field).Raw, field)
			}
		})
	}
}

func TestSummaryLeavesUnrecognizedContentIntact(t *testing.T) {
	for _, test := range []struct {
		resource, input string
	}{
		{"models", `{"id":"example","object":"model","new_field":"future information"}`},
		{"models", `{"id":"example","object":"model","new_field":null}`},
		{"models", `{"id":"example","object":"model","warning":"Review migration."}`},
		{"models", `{"id":"first","id":"second","object":"model"}`},
		{"files", `{"id":"example","object":"file","content":"Actual file content"}`},
		{"files", `{"id":"example","object":"file","annotations":[{"citation":"source"}]}`},
		{"files", `{"id":"example","object":"file","error":{"message":"Failure"}}`},
		{"models", `{"id":123,"object":"model"}`},
		{"files", `{"id":"example","object":"model"}`},
		{"models", `{"id":"example","object":"new_model_shape"}`},
		{"responses", `{"id":"example","object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}`},
		{"beta.threads.messages", `{"id":"example","object":"thread.message","content":[{"type":"text","text":{"value":"Answer","annotations":[]}}]}`},
		{"models", `{"object":"list","data":[{"id":"example","object":"model"}]}`},
	} {
		t.Run(test.input, func(t *testing.T) {
			value := gjson.Parse(test.input)
			got, ok, _ := summarize(value, transformers.Route{Operation: "(resource) " + test.resource + " > (method) retrieve", OutputKind: transformers.OutputResponse})
			require.False(t, ok)
			require.False(t, got.Exists())
			require.Equal(t, test.input, value.Raw)
		})
	}
	_, ok, _ := summarize(gjson.Parse(`{"id":"model_example","object":"model"}`), transformers.Route{Operation: "(resource) models > (method) retrieve", OutputKind: transformers.OutputStreamEvent})
	require.False(t, ok, "stream events must not be summarized")
}

func TestSummaryDeletion(t *testing.T) {
	for _, test := range []struct {
		resource, object, noun string
	}{
		{"models", "model", "model"},
		{"files", "file", "file"},
		{"vector_stores", "vector_store.deleted", "vector store"},
		{"vector_stores.files", "vector_store.file.deleted", "vector store file"},
		{"beta.assistants", "assistant.deleted", "assistant"},
		{"beta.threads", "thread.deleted", "thread"},
		{"beta.threads.messages", "thread.message.deleted", "message"},
		{"conversations", "conversation.deleted", "conversation"},
	} {
		t.Run(test.resource, func(t *testing.T) {
			route := transformers.Route{Operation: "(resource) " + test.resource + " > (method) delete", OutputKind: transformers.OutputResponse}
			input := `{"id":"example-123","object":"` + test.object + `","deleted":true}`
			got, ok, _ := summarize(gjson.Parse(input), route)
			require.True(t, ok)
			require.Equal(t, "Deleted "+test.noun+".", got.Get("result").Str)
			require.Equal(t, "example-123", got.Get("id").Str)
			require.True(t, got.Get("deleted").Bool())
			got, ok, _ = summarize(gjson.Parse(strings.Replace(input, "true", "false", 1)), route)
			require.True(t, ok)
			require.Equal(t, "Deletion was not confirmed.", got.Get("result").Str)
			require.False(t, got.Get("deleted").Bool())
			_, ok, _ = summarize(gjson.Parse(strings.TrimSuffix(input, "}")+`,"warning":"Additional action needed."}`), route)
			require.False(t, ok)
		})
	}
}
