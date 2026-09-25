package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const resourceSummaryHint = "Summary; use --format json for full data."

func TestMainResourceSummariesRetainActionFields(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		args, want       []string
	}{
		{"model", "/models/model_synthetic", `{"id":"model_synthetic","object":"model","created":17,"owned_by":"synthetic","shutdown_date":"2030-01-01"}`,
			[]string{"models", "retrieve", "model_synthetic"}, []string{"ID: model_synthetic", "Owned by: synthetic", "Shutdown date: 2030-01-01"}},
		{"file", "/files/file_synthetic", `{"id":"file_synthetic","object":"file","created_at":17,"filename":"資料-é.txt","purpose":"assistants","bytes":0,"status":"processed","status_details":null,"expires_at":31}`,
			[]string{"files", "retrieve", "file_synthetic"}, []string{"ID: file_synthetic", "Filename: 資料-é.txt", "Purpose: assistants", "Bytes: 0", "Status: processed", "Expires at: 31"}},
		{"batch", "/batches/batch_synthetic", `{"id":"batch_synthetic","object":"batch","created_at":17,"status":"completed","request_counts":{"completed":2,"failed":1,"total":3},"input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error","endpoint":"/v1/responses","metadata":{"note":"hidden metadata"}}`,
			[]string{"batches", "retrieve", "batch_synthetic"}, []string{"ID: batch_synthetic", "Status: completed", "Completed: 2", "Failed: 1", "Input file ID: file_input", "Output file ID: file_output", "Error file ID: file_error", "Endpoint: /v1/responses"}},
		{"vector store", "/vector_stores/vs_synthetic", `{"id":"vs_synthetic","object":"vector_store","created_at":17,"name":"Synthetic store","status":"completed","file_counts":{"completed":3,"failed":1},"usage_bytes":9007199254740993,"expires_at":31}`,
			[]string{"vector-stores", "retrieve", "vs_synthetic"}, []string{"ID: vs_synthetic", "Name: Synthetic store", "Status: completed", "Completed: 3", "Failed: 1", "Usage bytes: 9007199254740993", "Expires at: 31"}},
		{"vector file", "/vector_stores/vs_synthetic/files/file_synthetic", `{"id":"file_synthetic","object":"vector_store.file","created_at":17,"vector_store_id":"vs_synthetic","status":"failed","usage_bytes":0,"last_error":{"code":"synthetic_error","message":"Synthetic failure"},"attributes":{"enabled":false}}`,
			[]string{"vector-stores:files", "retrieve", "--vector-store-id", "vs_synthetic", "--file-id", "file_synthetic"}, []string{"ID: file_synthetic", "Vector store ID: vs_synthetic", "Status: failed", "Usage bytes: 0", "Code: synthetic_error", "Message: Synthetic failure", "Enabled: false"}},
		{"vector file batch", "/vector_stores/vs_synthetic/file_batches/vsfb_synthetic", `{"id":"vsfb_synthetic","object":"vector_store.files_batch","created_at":17,"vector_store_id":"vs_synthetic","status":"completed","file_counts":{"completed":2,"failed":0}}`,
			[]string{"vector-stores:file-batches", "retrieve", "--vector-store-id", "vs_synthetic", "--batch-id", "vsfb_synthetic"}, []string{"ID: vsfb_synthetic", "Vector store ID: vs_synthetic", "Status: completed", "Completed: 2", "Failed: 0"}},
		{"fine tuning job", "/fine_tuning/jobs/ftjob_synthetic", `{"id":"ftjob_synthetic","object":"fine_tuning.job","created_at":17,"status":"succeeded","model":"model_base","fine_tuned_model":"model_tuned","training_file":"file_training","validation_file":"file_validation","result_files":["file_result"],"trained_tokens":42,"error":null,"estimated_finish":null}`,
			[]string{"fine-tuning:jobs", "retrieve", "ftjob_synthetic"}, []string{"ID: ftjob_synthetic", "Status: succeeded", "Model: model_base", "Fine tuned model: model_tuned", "Training file: file_training", "Validation file: file_validation", "file_result", "Trained tokens: 42"}},
		{"assistant", "/assistants/asst_synthetic", `{"id":"asst_synthetic","object":"assistant","created_at":17,"name":"Synthetic assistant","model":"model_synthetic","description":"Synthetic description","instructions":"hidden instructions","tools":[]}`,
			[]string{"beta:assistants", "retrieve", "asst_synthetic"}, []string{"ID: asst_synthetic", "Name: Synthetic assistant", "Model: model_synthetic", "Description: Synthetic description"}},
		{"thread run", "/threads/thread_synthetic/runs/run_synthetic", `{"id":"run_synthetic","object":"thread.run","created_at":17,"status":"requires_action","thread_id":"thread_synthetic","assistant_id":"asst_synthetic","model":"model_synthetic","required_action":{"type":"submit_tool_outputs","submit_tool_outputs":{"tool_calls":[{"id":"call_synthetic","type":"function","function":{"name":"synthetic_function","arguments":"{}"}}]}},"last_error":null,"usage":{"total_tokens":12},"expires_at":31}`,
			[]string{"beta:threads:runs", "retrieve", "--thread-id", "thread_synthetic", "--run-id", "run_synthetic"}, []string{"ID: run_synthetic", "Status: requires_action", "Thread ID: thread_synthetic", "Assistant ID: asst_synthetic", "Type: submit_tool_outputs", "ID: call_synthetic", "Name: synthetic_function", "Arguments: {}", "Total tokens: 12", "Expires at: 31"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != tc.path || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, tc.args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
			for _, want := range append(tc.want, resourceSummaryHint) {
				if !strings.Contains(got.stdout, want+"\n") {
					t.Errorf("missing %q in %q", want, got.stdout)
				}
			}
			for _, hidden := range []string{"Object:", "Created:", "Created at:", "hidden metadata", "hidden instructions", "(null)"} {
				if strings.Contains(got.stdout, hidden) {
					t.Errorf("summary retained %q: %q", hidden, got.stdout)
				}
			}
		})
	}
}

func TestMainResourceSummaryListFilesInVectorBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/vector_stores/vs_synthetic/file_batches/vsfb_synthetic/files" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"file_synthetic","object":"vector_store.file","created_at":17,"vector_store_id":"vs_synthetic","status":"completed","usage_bytes":12}],"has_more":false}`)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "vector-stores:file-batches", "list-files", "--vector-store-id", "vs_synthetic", "--batch-id", "vsfb_synthetic")
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Vector store ID: vs_synthetic\n") || !strings.Contains(got.stdout, resourceSummaryHint) || strings.Contains(got.stdout, "Created at:") {
		t.Fatalf("list-files did not summarize its file item: %+v", got)
	}
}

func TestMainResourceSummaryPaginationPreservesOrderDuplicatesAndLimits(t *testing.T) {
	for _, tc := range []struct {
		limit string
		ids   []string
		calls int
	}{
		{"-1", []string{"file_a", "file_b", "file_b", "file_c"}, 2},
		{"3", []string{"file_a", "file_b", "file_b"}, 2},
		{"1", []string{"file_a"}, 1},
		{"0", nil, 1},
	} {
		t.Run(tc.limit, func(t *testing.T) {
			var mu sync.Mutex
			var cursors []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cursor := r.URL.Query().Get("after")
				mu.Lock()
				cursors = append(cursors, cursor)
				mu.Unlock()
				if r.Method != http.MethodGet || r.URL.Path != "/files" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("order") != "asc" || r.URL.Query().Get("purpose") != "assistants" {
					t.Errorf("request options changed: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				switch cursor {
				case "file_before":
					io.WriteString(w, resourceFilePage([]string{"file_a", "file_b"}, true))
				case "file_b":
					io.WriteString(w, resourceFilePage([]string{"file_b", "file_c"}, false))
				default:
					t.Errorf("unexpected pagination cursor: %q", cursor)
					http.Error(w, "unexpected cursor", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "files", "list", "--after", "file_before", "--limit", "2", "--order", "asc", "--purpose", "assistants", "--max-items", tc.limit)
			mu.Lock()
			actualCursors := slices.Clone(cursors)
			mu.Unlock()
			wantCursors := []string{"file_before", "file_b"}[:tc.calls]
			if got.code != 0 || got.stderr != "" || !slices.Equal(actualCursors, wantCursors) {
				t.Fatalf("result=%+v cursors=%q; want %q", got, actualCursors, wantCursors)
			}
			var ids []string
			for _, line := range strings.Split(got.stdout, "\n") {
				if id, ok := strings.CutPrefix(line, "ID: "); ok {
					ids = append(ids, id)
				}
			}
			if !slices.Equal(ids, tc.ids) || strings.Contains(got.stdout, "Created at:") {
				t.Fatalf("summary records changed: ids=%q want=%q output=%q", ids, tc.ids, got.stdout)
			}
			if len(ids) == 0 && got.stdout != "" || len(ids) > 0 && !strings.Contains(got.stdout, resourceSummaryHint) {
				t.Fatalf("incorrect summary notice or zero limit output: %q", got.stdout)
			}
		})
	}
}

func resourceFilePage(ids []string, more bool) string {
	var items []string
	for _, id := range ids {
		items = append(items, fmt.Sprintf(`{"id":%q,"object":"file","created_at":17,"filename":"synthetic.txt","bytes":5,"purpose":"assistants"}`, id))
	}
	last := ""
	if len(ids) > 0 {
		last = ids[len(ids)-1]
	}
	return fmt.Sprintf(`{"object":"list","data":[%s],"has_more":%t,"last_id":%q}`, strings.Join(items, ","), more, last)
}

func TestMainResourceSummaryEmptyList(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, resourceFilePage(nil, false))
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "files", "list")
	if got.code != 0 || got.stderr != "" || got.stdout != "No results.\n" || requests.Load() != 1 {
		t.Fatalf("empty list changed: %+v requests=%d", got, requests.Load())
	}
}

func TestMainResourceSummaryUnknownFieldsAndShapesFallBack(t *testing.T) {
	for _, body := range []string{
		`{"id":"model_synthetic","object":"model","created":17`,
		`{"id":"model_synthetic","object":"model" "created":17}`,
		`{"id":"model_synthetic","object":"model","created":17,"future_field":null}`,
		`{"id":"model_synthetic","object":"model","created":17,"future_field":{"note":"retained"}}`,
		`{"id":"model_synthetic","object":"model","created":17,"created":18}`,
		`{"object":"model","created":17}`,
		`{"id":null,"object":"model","created":17}`,
		`{"id":"","object":"model","created":17}`,
		`{"id":12,"object":"model","created":17}`,
		`{"id":"model_synthetic","created":17}`,
		`{"id":"model_synthetic","object":null,"created":17}`,
		`{"id":"model_synthetic","object":"unfamiliar","created":17}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "models", "retrieve", "model_synthetic")
			// Explicit extraction of the whole object bypasses summarization and
			// provides the full labeled-output control, including duplicate keys.
			full := runReadableCommand(t, server, "--format", "text", "--transform", "@this", "models", "retrieve", "model_synthetic")
			if got.code != 0 || got.stderr != "" || got != full || !strings.Contains(got.stdout, "Created: 17\n") || strings.Contains(got.stdout, resourceSummaryHint) {
				t.Fatalf("fallback=%+v full=%+v", got, full)
			}
		})
	}
}

func TestMainResourceSummaryExplicitFormatsAndExtractionKeepFullData(t *testing.T) {
	const body = `{"id":"model_synthetic","object":"model","created":17,"owned_by":"synthetic"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	for _, format := range []string{"auto", "text", "TeXt"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--format", format, "models", "retrieve", "model_synthetic")
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, resourceSummaryHint) || strings.Contains(got.stdout, "Created:") {
				t.Fatalf("explicit readable format did not summarize: %+v", got)
			}
		})
	}
	for _, format := range []string{"json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--format", format, "models", "retrieve", "model_synthetic")
			if got.code != 0 || format != "explore" && got.stderr != "" || strings.Contains(got.stdout, resourceSummaryHint) {
				t.Fatalf("unexpected explicit format result: %+v", got)
			}
			switch format {
			case "json", "jsonl", "raw", "explore":
				var expected, actual any
				if err := json.Unmarshal([]byte(body), &expected); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(got.stdout), &actual); err != nil || !reflect.DeepEqual(actual, expected) {
					t.Fatalf("full API JSON changed: %q error=%v", got.stdout, err)
				}
				if format == "raw" && got.stdout != body+"\n" || format == "jsonl" && strings.Count(got.stdout, "\n") != 1 {
					t.Fatalf("explicit %s bytes changed: %q", format, got.stdout)
				}
			default:
				if !strings.Contains(strings.ToLower(got.stdout), "created") || !strings.Contains(got.stdout, "17") {
					t.Fatalf("explicit %s lost omitted field: %q", format, got.stdout)
				}
			}
			if format == "explore" && !strings.Contains(got.stderr, "falling back to 'json'") {
				t.Fatalf("missing explore warning: %q", got.stderr)
			}
		})
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--transform", "created"}, "17\n"},
		{[]string{"--format", "auto", "--transform", "owned_by"}, "\"synthetic\"\n"},
		{[]string{"--transform", "owned_by", "--raw-output"}, "synthetic\n"},
		{[]string{"--format", "text", "--transform", "@this"}, "ID: model_synthetic\nObject: model\nCreated: 17\nOwned by: synthetic\n"},
		{[]string{"--format", "text", "--transform", "absent"}, "ID: model_synthetic\nObject: model\nCreated: 17\nOwned by: synthetic\n"},
		{[]string{"--format", "text", "--raw-output"}, "ID: model_synthetic\nObject: model\nCreated: 17\nOwned by: synthetic\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			args := append(slices.Clone(tc.args), "models", "retrieve", "model_synthetic")
			if got := runReadableCommand(t, server, args...); got.code != 0 || got.stderr != "" || got.stdout != tc.want {
				t.Fatalf("got %+v; want %q", got, tc.want)
			}
		})
	}
}

func TestMainResourceSummaryListFormatsAndExtractionKeepFullItems(t *testing.T) {
	const item = `{"id":"file_synthetic","object":"file","created_at":17,"filename":"synthetic.txt","bytes":5,"purpose":"assistants"}`
	const page = `{"object":"list","data":[` + item + `,` + item + `],"has_more":false,"last_id":"file_synthetic"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, page)
	}))
	defer server.Close()
	for _, format := range []string{"json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--format", format, "files", "list")
			if got.code != 0 || format != "explore" && got.stderr != "" || strings.Contains(got.stdout, resourceSummaryHint) {
				t.Fatalf("unexpected explicit list result: %+v", got)
			}
			switch format {
			case "raw":
				if got.stdout != page+"\n" {
					t.Fatalf("raw page envelope changed: %q", got.stdout)
				}
			case "json", "jsonl", "explore":
				var expected any
				if err := json.Unmarshal([]byte(item), &expected); err != nil {
					t.Fatal(err)
				}
				decoder := json.NewDecoder(strings.NewReader(got.stdout))
				for range 2 {
					var actual any
					if err := decoder.Decode(&actual); err != nil || !reflect.DeepEqual(actual, expected) {
						t.Fatalf("full API item changed: output=%q error=%v", got.stdout, err)
					}
				}
				var extra any
				if err := decoder.Decode(&extra); err != io.EOF {
					t.Fatalf("unexpected extra output: %q error=%v", got.stdout, err)
				}
				if format == "jsonl" && strings.Count(got.stdout, "\n") != 2 {
					t.Fatalf("jsonl must contain exactly two records: %q", got.stdout)
				}
			default:
				if strings.Count(strings.ToLower(got.stdout), "created_at") != 2 {
					t.Fatalf("explicit %s lost omitted fields: %q", format, got.stdout)
				}
			}
		})
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--transform", "created_at"}, "17\n17\n"},
		{[]string{"--transform", "filename", "--raw-output"}, "synthetic.txt\nsynthetic.txt\n"},
		{[]string{"--format", "text", "--transform", "@this"}, "ID: file_synthetic\nObject: file\nCreated at: 17\nFilename: synthetic.txt\nBytes: 5\nPurpose: assistants\n\nID: file_synthetic\nObject: file\nCreated at: 17\nFilename: synthetic.txt\nBytes: 5\nPurpose: assistants\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got := runReadableCommand(t, server, append(slices.Clone(tc.args), "files", "list")...)
			if got.code != 0 || got.stderr != "" || got.stdout != tc.want {
				t.Fatalf("list extraction changed: %+v want=%q", got, tc.want)
			}
		})
	}
}

func TestMainResourceSummaryLeavesOtherOperationsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		args                     []string
	}{
		{"create", http.MethodPost, "/vector_stores", `{"id":"vs_synthetic","object":"vector_store","created_at":17,"name":"Synthetic store"}`, []string{"vector-stores", "create", "--name", "Synthetic store"}},
		{"delete", http.MethodDelete, "/files/file_synthetic", `{"id":"file_synthetic","object":"file","deleted":true}`, []string{"files", "delete", "file_synthetic"}},
		{"cancel", http.MethodPost, "/batches/batch_synthetic/cancel", `{"id":"batch_synthetic","object":"batch","created_at":17,"status":"cancelling"}`, []string{"batches", "cancel", "batch_synthetic"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, tc.args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Object:") || strings.Contains(got.stdout, resourceSummaryHint) || strings.Contains(got.stdout, "Deleted file.") {
				t.Fatalf("operation outside summary scope changed: %+v", got)
			}
		})
	}
}

func TestMainResourceSummaryPreservesLargeActionArguments(t *testing.T) {
	// The size is a regression probe for arbitrary buffering limits, not a new
	// maximum. Keep this case sequential with other large response tests.
	argument := strings.Repeat("x", 18<<20) + "synthetic-large-argument-end"
	body := `{"id":"run_synthetic","object":"thread.run","created_at":17,"status":"requires_action","thread_id":"thread_synthetic","assistant_id":"asst_synthetic","model":"model_synthetic","required_action":{"type":"submit_tool_outputs","submit_tool_outputs":{"tool_calls":[{"id":"call_synthetic","type":"function","function":{"name":"synthetic_function","arguments":"` + argument + `"}}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/threads/thread_synthetic/runs/run_synthetic" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "beta:threads:runs", "retrieve", "--thread-id", "thread_synthetic", "--run-id", "run_synthetic")
	if got.code != 0 || got.stderr != "" || strings.Count(got.stdout, argument) != 1 || !strings.Contains(got.stdout, resourceSummaryHint) || strings.Contains(got.stdout, "Created at:") {
		t.Fatalf("large action summary failed: exit=%d stdout bytes=%d stderr=%q", got.code, len(got.stdout), got.stderr)
	}
}

func TestMainResourceSummaryPreservesPartialPageOnFailure(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("after") == "" {
					io.WriteString(w, resourceFilePage([]string{"file_first"}, true))
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":{"message":"synthetic second-page failure","type":"invalid_request_error"}}`)
			}))
			defer server.Close()
			args := []string{"files", "list"}
			if format == "json" {
				args = append([]string{"--format-error", "json"}, args...)
			}
			got := runReadableCommand(t, server, args...)
			if got.code != 1 || requests.Load() != 2 || strings.Count(got.stdout, "ID: file_first\n") != 1 || !strings.Contains(got.stdout, resourceSummaryHint) || strings.Contains(got.stdout, "Created at:") {
				t.Fatalf("partial output or pagination failure changed: %+v requests=%d", got, requests.Load())
			}
			if format == "json" {
				var failure map[string]any
				if err := json.Unmarshal([]byte(got.stderr), &failure); err != nil || failure["message"] != "synthetic second-page failure" || failure["type"] != "invalid_request_error" {
					t.Fatalf("explicit API error details lost: %q (%v)", got.stderr, err)
				}
			} else if !strings.Contains(got.stderr, "The API rejected the request.") || !strings.Contains(got.stderr, "--format-error json") || strings.Contains(got.stderr, "synthetic second-page failure") {
				t.Fatalf("expected safe guidance with explicit error-details option: %q", got.stderr)
			}
		})
	}
}

func TestMainResourceSummaryWritesBeforeNextPageArrives(t *testing.T) {
	release := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			io.WriteString(w, resourceFilePage([]string{"file_first"}, true))
			return
		}
		select {
		case <-release:
			io.WriteString(w, resourceFilePage([]string{"file_second"}, false))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "files", "list")
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-resource-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		if child.ProcessState == nil {
			child.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	first := make(chan string, 1)
	go func() {
		var output strings.Builder
		for {
			line, err := reader.ReadString('\n')
			output.WriteString(line)
			if strings.HasPrefix(line, "ID:") || err != nil {
				first <- output.String()
				return
			}
		}
	}()
	select {
	case output := <-first:
		if !strings.Contains(output, "ID: file_first\n") {
			t.Fatalf("first page missing: %q", output)
		}
	case <-ctx.Done():
		t.Fatal("first page was buffered while the next page was withheld")
	}
	release <- struct{}{}
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil || stderr.Len() != 0 || !strings.Contains(string(rest), "ID: file_second\n") {
		t.Fatalf("completion error=%v stderr=%q output=%q", err, stderr.String(), rest)
	}
}
