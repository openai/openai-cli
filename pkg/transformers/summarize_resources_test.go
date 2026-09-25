package transformers

import (
	"context"
	"strings"
	"testing"

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
			for _, kind := range []OutputKind{OutputResponse, OutputPageItem} {
				method := "retrieve"
				if kind == OutputPageItem {
					method = "list"
				}
				if test.resource == "vector_stores.file_batches" && kind == OutputPageItem {
					continue
				}
				input := gjson.Parse(test.input)
				got, omitted, err := SummarizeResource(context.Background(), input, Route{Operation: "(resource) " + test.resource + " > (method) " + method, OutputKind: kind})
				require.NoError(t, err)
				require.True(t, omitted)
				require.True(t, got.Exists())
				require.Equal(t, test.want, got.Raw)
				require.Equal(t, test.input, input.Raw, "original API value must stay unchanged")
			}
		})
	}
}

func TestSummaryRequiresListGetRoute(t *testing.T) {
	value := gjson.Parse(`{"id":"model_example","object":"model","created":17,"owned_by":"synthetic"}`)
	for _, method := range []string{"", "retrieve", "list", "create", "update", "delete", "cancel", "pause", "resume", "retrieve_extra"} {
		for _, kind := range []OutputKind{OutputUnspecified, OutputResponse, OutputPageItem, OutputStreamEvent} {
			route := Route{Operation: "(resource) models > (method) " + method, OutputKind: kind}
			got, _, err := SummarizeResource(t.Context(), value, route)
			require.NoError(t, err)
			require.Equal(t, method == "retrieve" && kind == OutputResponse || method == "list" && kind == OutputPageItem, got.Exists(), "%+v", route)
		}
	}
	for _, operation := range []string{"models.retrieve", "models > (method) retrieve", "(resource) models > (method) retrieve > extra"} {
		got, _, err := SummarizeResource(t.Context(), value, Route{operation, OutputResponse})
		require.NoError(t, err)
		require.False(t, got.Exists())
	}
	file := gjson.Parse(`{"id":"file_example","object":"vector_store.file","vector_store_id":"vs_example","status":"failed","last_error":{"code":"unsupported_file"}}`)
	got, omitted, err := SummarizeResource(t.Context(), file, Route{"(resource) vector_stores.file_batches > (method) list_files", OutputPageItem})
	require.NoError(t, err)
	require.True(t, omitted)
	require.Equal(t, file.Get("last_error").Raw, got.Get("last_error").Raw)
}

func TestSummaryEmptyAndMissingFields(t *testing.T) {
	route := Route{"(resource) files > (method) retrieve", OutputResponse}
	for _, input := range []string{
		`{"id":"file_example","object":"file"}`,
		`{"id":"file_example","object":"file","filename":"","purpose":null,"status":[],"status_details":{}}`,
	} {
		got, omitted, err := SummarizeResource(t.Context(), gjson.Parse(input), route)
		require.NoError(t, err)
		require.True(t, omitted)
		require.Equal(t, `{"id":"file_example"}`, got.Raw)
	}
	for _, input := range []string{
		`{}`, `null`, `[]`, `"file"`, `{"object":"file"}`,
		`{"id":null,"object":"file"}`, `{"id":"","object":"file"}`,
		`{"id":"file_example","object":null}`, `{"id":"file_example"}`,
		`{"id":"file_example","object":"file","object":"file"}`,
		`{"id":"file_example","object":"file","created_at":1,"created_at":2}`,
		`{"id":"file_example","object":"file","deleted":true}`,
	} {
		got, omitted, err := SummarizeResource(t.Context(), gjson.Parse(input), route)
		require.NoError(t, err)
		require.False(t, got.Exists(), input)
		require.False(t, omitted)
	}
	input := gjson.Parse(`{"id":"file_example","object":"file","bytes":0,"status_details":false}`)
	got, _, err := SummarizeResource(t.Context(), input, route)
	require.NoError(t, err)
	require.Equal(t, `{"id":"file_example","bytes":0,"status_details":false}`, got.Raw)
}

func TestSummaryMalformedJSONFallsBack(t *testing.T) {
	for _, input := range []string{
		`{"id":"file_example","object":"file","created_at":17`,
		`{"id":"file_example","object":"file" "created_at":17}`,
		`{"id":"file_example","object":"file","created_at":wat}`,
		`{"id":"file_example","object":"file","filename":"invalid\x1b"}`,
		`{"id":"file_example","object":"file","status_details":{"message":"incomplete"}`,
	} {
		t.Run(input, func(t *testing.T) {
			value := gjson.Parse(input)
			require.False(t, gjson.Valid(value.Raw))
			got, omitted, err := SummarizeResource(t.Context(), value, Route{"(resource) files > (method) retrieve", OutputResponse})
			require.NoError(t, err)
			require.False(t, got.Exists(), "malformed records must retain full output")
			require.False(t, omitted)
			require.Equal(t, input, value.Raw)
		})
	}
}

func TestSummaryLargeActionAndCancellation(t *testing.T) {
	route := Route{"(resource) beta.threads.runs > (method) retrieve", OutputResponse}
	// A large synthetic tool argument remains intact without a new output limit.
	argument := strings.Repeat("synthetic-", 2*1024*1024)
	value := gjson.Parse(`{"id":"run_example","object":"thread.run","status":"requires_action","required_action":{"arguments":"` + argument + `"},"created_at":17}`)
	got, _, err := SummarizeResource(t.Context(), value, route)
	require.NoError(t, err)
	require.Equal(t, argument, got.Get("required_action.arguments").Str)

	for _, after := range []int32{1, 4, 10} {
		ctx, cancel := context.WithCancel(t.Context())
		polls := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
		got, omitted, err := SummarizeResource(polls, value, route)
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, got.Exists())
		require.False(t, omitted)
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
			got, _, err := SummarizeResource(context.Background(), value, Route{Operation: "(resource) " + test.resource + " > (method) retrieve", OutputKind: OutputResponse})
			require.NoError(t, err)
			require.True(t, got.Exists())
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
			got, _, err := SummarizeResource(context.Background(), value, Route{Operation: "(resource) " + test.resource + " > (method) retrieve", OutputKind: OutputResponse})
			require.NoError(t, err)
			require.False(t, got.Exists())
			require.Equal(t, test.input, value.Raw)
		})
	}
	got, _, err := SummarizeResource(context.Background(), gjson.Parse(`{"id":"model_example","object":"model"}`), Route{Operation: "(resource) models > (method) retrieve", OutputKind: OutputStreamEvent})
	require.NoError(t, err)
	require.False(t, got.Exists(), "stream events must not be summarized")
}
