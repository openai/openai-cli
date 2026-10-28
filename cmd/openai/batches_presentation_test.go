package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const batchesPresentationResponse = `{"id":"batch_synthetic","object":"batch","status":"completed","completed_at":1700000061,"created_at":1700000000,"finalizing_at":1700000050,"in_progress_at":1700000010,"cancelled_at":null,"completion_window":"24h","request_counts":{"total":3,"completed":3,"failed":0},"input_file_id":"file_input","output_file_id":"file_output","error_file_id":null,"endpoint":"/v1/responses","expires_at":1700086400,"errors":{"data":[{"code":"synthetic_detail","line":9007199254740993,"message":"detail\u001b[31m"}]},"usage":{"total_tokens":9007199254740993},"metadata":{"label":"資料-é\u001b[31m"}}`

func TestMainDispatchBatchesPresentation(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, status string
		args                       []string
	}{
		{"create", http.MethodPost, "/batches", "validating", []string{"batches", "create", "--input-file-id", "file_input", "--endpoint", "/v1/responses", "--completion-window", "24h"}},
		{"retrieve", http.MethodGet, "/batches/batch_synthetic", "completed", []string{"batches", "retrieve", "batch_synthetic"}},
		{"cancel", http.MethodPost, "/batches/batch_synthetic/cancel", "cancelling", []string{"batches", "cancel", "batch_synthetic"}},
		{"retrieve wait", http.MethodGet, "/batches/batch_synthetic", "completed", []string{"batches", "retrieve", "batch_synthetic", "--wait"}},
		{"wait", http.MethodGet, "/batches/batch_synthetic", "completed", []string{"batches", "wait", "batch_synthetic"}},
		{"list", http.MethodGet, "/batches", "completed", []string{"batches", "list", "--limit", "1", "--max-items", "-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := strings.Replace(batchesPresentationResponse, `"status":"completed"`, `"status":"`+tc.status+`"`, 1)
			if tc.status != "completed" {
				response = strings.Replace(response, `"completed_at":1700000061,`, "", 1)
			}
			if tc.name == "list" {
				response = `{"object":"list","data":[` + response + `],"has_more":false}`
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if tc.name == "create" {
					var body map[string]any
					want := map[string]any{"input_file_id": "file_input", "endpoint": "/v1/responses", "completion_window": "24h"}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, want) {
						t.Errorf("create request changed: %v (%v)", body, err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, response)
			}))
			defer server.Close()
			for _, options := range [][]string{nil, {"--format", "text"}, {"--quiet"}, {"--format-error", "json"}} {
				got := runReadableCommand(t, server, append(slices.Clone(options), tc.args...)...)
				wantStderr := resourceSummaryHint + "\n"
				if slices.Contains(options, "--quiet") || slices.Contains(options, "--format-error") {
					wantStderr = ""
				}
				if got.code != 0 || got.stderr != wantStderr {
					t.Fatalf("options=%v result=%+v", options, got)
				}
				for _, want := range []string{"ID: batch_synthetic", "Status: " + tc.status, "Created at: 2023-11-14T22:13:20Z", "Completion window: 24h", "Total: 3", "Failed: 0", "Input file ID: file_input", "Output file ID: file_output", "Total tokens: 9007199254740993", "Line: 9007199254740993", `Label: 資料-é\u001b[31m`, `Message: detail\u001b[31m`} {
					if !strings.Contains(got.stdout, want+"\n") {
						t.Errorf("missing %q in %q", want, got.stdout)
					}
				}
				previous := -1
				for _, want := range []string{"Created at:", "In progress at:", "Finalizing at:", "Expires at:"} {
					index := strings.Index(got.stdout, want)
					if index <= previous {
						t.Errorf("lifecycle order changed at %s: %q", want, got.stdout)
					}
					previous = index
				}
				if strings.Contains(got.stdout, "Completion duration: 1m 1s\n") != (tc.status == "completed") {
					t.Errorf("incorrect completion duration: %q", got.stdout)
				}
				for _, hidden := range []string{"Object:", "(null)", "Cancelled at:", "Error file ID:", resourceSummaryHint, "\x1b"} {
					if strings.Contains(got.stdout, hidden) {
						t.Errorf("unexpected %q in %q", hidden, got.stdout)
					}
				}
			}
			if requests.Load() != 4 {
				t.Fatalf("presentation changed request count: %d", requests.Load())
			}
		})
	}
}

func TestMainDispatchBatchesPresentationExplicitModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"retrieve", []string{"batches", "retrieve", "batch_synthetic"}},
		{"list", []string{"batches", "list"}},
		{"create", []string{"batches", "create", "--input-file-id", "file_input", "--endpoint", "/v1/responses", "--completion-window", "24h"}},
		{"cancel", []string{"batches", "cancel", "batch_synthetic"}},
		{"retrieve wait", []string{"batches", "retrieve", "batch_synthetic", "--wait"}},
		{"wait", []string{"batches", "wait", "batch_synthetic"}},
	} {
		list, response, args := tc.name == "list", batchesPresentationResponse, tc.args
		if list {
			response = `{"object":"list","data":[` + response + `],"has_more":false}`
		}
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, response)
			}))
			defer server.Close()
			for _, format := range []string{"json", "jsonl", "yaml", "raw", "pretty", "explore"} {
				t.Run(format, func(t *testing.T) {
					got := runReadableCommand(t, server, append([]string{"--format", format}, args...)...)
					if got.code != 0 || format != "explore" && got.stderr != "" || strings.Contains(got.stderr, resourceSummaryHint) {
						t.Fatalf("unexpected explicit format result: %+v", got)
					}
					if format == "explore" && !strings.Contains(got.stderr, "falling back to 'json'") {
						t.Fatalf("missing explorer fallback warning: %q", got.stderr)
					}
					if format == "raw" {
						if got.stdout != response+"\n" {
							t.Fatalf("raw bytes changed: %q", got.stdout)
						}
						return
					}
					if format == "pretty" {
						for _, want := range []string{"created_at", "1700000000", "completed_at", "1700000061", "9007199254740993"} {
							if !strings.Contains(got.stdout, want) {
								t.Errorf("pretty output lost %q: %q", want, got.stdout)
							}
						}
						return
					}
					if format == "yaml" {
						// Compare bytes directly: goccy rejects the encoder's valid \x escapes.
						const want = `id: batch_synthetic
object: batch
status: completed
completed_at: 1700000061
created_at: 1700000000
finalizing_at: 1700000050
in_progress_at: 1700000010
cancelled_at: null
completion_window: 24h
request_counts:
  total: 3
  completed: 3
  failed: 0
input_file_id: file_input
output_file_id: file_output
error_file_id: null
endpoint: /v1/responses
expires_at: 1700086400
errors:
  data:
    - code: synthetic_detail
      line: 9007199254740993
      message: "detail\x1B[31m"
usage:
  total_tokens: 9007199254740993
metadata:
  label: "資料-é\x1B[31m"
`
						if got.stdout != want {
							t.Fatalf("YAML changed API data: got %q; want %q", got.stdout, want)
						}
						return
					}
					want := batchesPresentationResponse
					if list && format == "json" {
						want = "[" + want + "]"
					}
					decode := func(raw string) any {
						t.Helper()
						decoder := json.NewDecoder(strings.NewReader(raw))
						decoder.UseNumber()
						var value any
						if err := decoder.Decode(&value); err != nil {
							t.Fatal(err)
						}
						var extra any
						if err := decoder.Decode(&extra); err != io.EOF {
							t.Fatalf("unexpected trailing data: %q (%v)", raw, err)
						}
						return value
					}
					if !reflect.DeepEqual(decode(got.stdout), decode(want)) {
						t.Fatalf("explicit %s changed API data: %q", format, got.stdout)
					}
				})
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(batchesPresentationResponse), &fields); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"created_at", "completed_at", "metadata", "usage", "errors", "input_file_id", "output_file_id", "error_file_id"} {
				got := runReadableCommand(t, server, append([]string{"--format", "jsonl", "--transform", field}, args...)...)
				if got.code != 0 || got.stderr != "" || got.stdout != string(fields[field])+"\n" {
					t.Errorf("extraction changed %s: %+v", field, got)
				}
			}
			full := runReadableCommand(t, server, append([]string{"--format", "text", "--transform", "@this"}, args...)...)
			for _, options := range [][]string{{"--format", "text", "--transform", "missing_field"}, {"--format", "text", "--raw-output"}} {
				got := runReadableCommand(t, server, append(slices.Clone(options), args...)...)
				if got != full || got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Created at: 1700000000\n") {
					t.Errorf("explicit readable bypass changed: result=%+v control=%+v", got, full)
				}
			}
			got := runReadableCommand(t, server, append([]string{"--transform", "id", "--raw-output"}, args...)...)
			if got.code != 0 || got.stderr != "" || got.stdout != "batch_synthetic\n" {
				t.Errorf("raw ID extraction changed: %+v", got)
			}
		})
	}
}

func TestMainDispatchBatchesPresentationFallback(t *testing.T) {
	for _, fields := range []string{
		`"created_at":17,"future_field":null`,
		`"created_at":17,"created_at":18`,
		`"created_at":"17"`, `"created_at":[]`, `"created_at":253402300800`,
		`"created_at":17,"status":"future_status"`,
	} {
		t.Run(fields, func(t *testing.T) {
			body := `{"id":"batch_synthetic","object":"batch",` + fields + `}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "batches", "retrieve", "batch_synthetic")
			full := runReadableCommand(t, server, "--format", "text", "--transform", "@this", "batches", "retrieve", "batch_synthetic")
			if got != full || got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Object: batch\n") {
				t.Fatalf("fallback changed API data: result=%+v control=%+v", got, full)
			}
		})
	}
}

func TestMainDispatchBatchesPresentationPartialPageFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			io.WriteString(w, `{"object":"list","data":[`+batchesPresentationResponse+`],"has_more":true,"last_id":"batch_synthetic"}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"synthetic second-page failure","type":"invalid_request_error"}}`)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "batches", "list", "--limit", "1", "--max-items", "-1")
	if got.code != 1 || requests.Load() != 2 || strings.Count(got.stdout, "ID: batch_synthetic\n") != 1 || strings.Count(got.stderr, resourceSummaryHint) != 1 || !strings.Contains(got.stdout, "Created at: 2023-11-14T22:13:20Z\n") || !strings.Contains(got.stderr, "The API rejected the request.") {
		t.Fatalf("partial Batch list lost output or failure: result=%+v requests=%d", got, requests.Load())
	}
}
