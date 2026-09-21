package cli_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestMainReadableManagementSummaries(t *testing.T) {
	for _, test := range []struct {
		name, route, payload string
		args, kept, omitted  []string
	}{
		{
			name: "completed batch", route: "GET /batches/batch_example",
			args:    []string{"batches", "retrieve", "batch_example"},
			payload: `{"id":"batch_example","object":"batch","status":"completed","endpoint":"/v1/responses","input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_failed_rows","request_counts":{"total":3,"completed":2,"failed":1},"completion_window":"24h","created_at":123,"completed_at":456,"metadata":{"internal_note":"Synthetic configuration is available through JSON."}}`,
			kept:    []string{"ID: batch_example", "Status: completed", "Completed: 2", "Failed: 1", "Output file ID: file_output", "Error file ID: file_failed_rows", "--format json"},
			omitted: []string{"Completion window:", "Created at:", "Completed at:", "Synthetic configuration is available through JSON."},
		},
		{
			name: "fine tuning result", route: "GET /fine_tuning/jobs/ftjob_example",
			args:    []string{"fine-tuning:jobs", "retrieve", "ftjob_example"},
			payload: `{"id":"ftjob_example","object":"fine_tuning.job","status":"succeeded","model":"gpt-example","fine_tuned_model":"ft:gpt-example:org:exact-id","training_file":"file_training","result_files":["file_result"],"trained_tokens":9007199254740993,"error":null,"seed":42,"created_at":123,"hyperparameters":{"n_epochs":1,"batch_size":5},"method":{"type":"supervised"},"metadata":{"internal_note":"Synthetic training configuration."}}`,
			kept:    []string{"ID: ftjob_example", "Status: succeeded", "Fine tuned model: ft:gpt-example:org:exact-id", "file_training", "file_result", "9007199254740993", "--format json"},
			omitted: []string{"Created at:", "Hyperparameters:", "Synthetic training configuration."},
		},
		{
			name: "run needs tool output", route: "GET /threads/thread_example/runs/run_example",
			args:    []string{"beta:threads:runs", "retrieve", "thread_example", "run_example"},
			payload: `{"id":"run_example","object":"thread.run","status":"requires_action","thread_id":"thread_example","assistant_id":"asst_example","model":"gpt-example","created_at":123,"instructions":"Synthetic assistant configuration.","required_action":{"type":"submit_tool_outputs","submit_tool_outputs":{"tool_calls":[{"id":"call_example","type":"function","function":{"name":"lookup","arguments":"{\"record\":\"example\"}"}}]}},"last_error":null,"metadata":{}}`,
			kept:    []string{"ID: run_example", "Status: requires_action", "Thread ID: thread_example", "Assistant ID: asst_example", "Required action:", "submit_tool_outputs", "call_example", "lookup", `{"record":"example"}`, "--format json"},
			omitted: []string{"Created at:", "Synthetic assistant configuration."},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := readableTestServer(t, test.route, "application/json", test.payload, http.StatusOK)
			got := runReadableMain(t, server.URL, nil, test.args...)
			assertReadableSuccess(t, got, test.kept...)
			for _, text := range test.omitted {
				if strings.Contains(got.stdout, text) {
					t.Errorf("summary includes low-priority configuration %q: %s", text, got.stdout)
				}
			}
			got = runReadableMain(t, server.URL, nil, append([]string{"--format", "json"}, test.args...)...)
			assertReadableProcessSuccess(t, got)
			assertReadableJSONValues(t, got.stdout, test.payload)
		})
	}
}

func TestMainReadableSummaryKeepsNewFieldsAndIssues(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		content       []string
	}{
		{
			name:    "unknown fields use complete rendering",
			payload: `{"id":"batch_example","object":"batch","status":"completed","created_at":123,"metadata":{"label":"Do not omit when using the fallback."},"future_results":{"text":"New result content stays visible.","precise_count":9007199254740993}}`,
			content: []string{"New result content stays visible.", "9007199254740993", "Created at: 123", "Do not omit when using the fallback."},
		},
		{
			name:    "failed batch details stay visible",
			payload: `{"id":"batch_example","object":"batch","status":"failed","created_at":123,"errors":{"data":[{"code":"invalid_request","message":"Synthetic request needs a model.","param":"model","line":9007199254740993}]}}`,
			content: []string{"Status: failed", "Errors:", "invalid_request", "Synthetic request needs a model.", "Param: model", "9007199254740993"},
		},
		{
			name:    "new warning is never hidden",
			payload: `{"id":"batch_example","object":"batch","status":"completed","created_at":123,"warning":"Review the rejected rows."}`,
			content: []string{"Warning: Review the rejected rows.", "Created at: 123"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := readableTestServer(t, "GET /batches/batch_example", "application/json", test.payload, http.StatusOK)
			got := runReadableMain(t, server.URL, nil, "batches", "retrieve", "batch_example")
			assertReadableSuccess(t, got, test.content...)
		})
	}
}

func TestMainReadableSummaryAppliesToListItems(t *testing.T) {
	const first = `{"id":"batch_first","object":"batch","status":"completed","input_file_id":"file_first","output_file_id":"file_result","request_counts":{"completed":2,"failed":0,"total":2},"created_at":123,"metadata":{"note":"Synthetic list configuration."}}`
	const second = `{"id":"batch_second","object":"batch","status":"in_progress","input_file_id":"file_second","request_counts":{"completed":0,"failed":0,"total":2},"created_at":456}`
	const payload = `{"object":"list","data":[` + first + `,` + second + `],"has_more":false,"first_id":"batch_first","last_id":"batch_second"}`
	server := readableTestServer(t, "GET /batches", "application/json", payload, http.StatusOK)
	got := runReadableMain(t, server.URL, nil, "batches", "list")
	assertReadableSuccess(t, got, "ID: batch_first", "Status: completed", "ID: batch_second", "Status: in_progress", "Completed: 0", "--format json")
	if strings.Contains(got.stdout, "Created at:") || strings.Contains(got.stdout, "Synthetic list configuration.") {
		t.Fatalf("list items bypassed summaries: %s", got.stdout)
	}
	got = runReadableMain(t, server.URL, nil, "--format", "json", "batches", "list")
	assertReadableProcessSuccess(t, got)
	assertReadableJSONValues(t, got.stdout, first, second)
}
