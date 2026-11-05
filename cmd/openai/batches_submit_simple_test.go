package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"
)

const simpleBatchInput = "{\"custom_id\":\"one\",\"method\":\"POST\",\"url\":\"/v1/responses\",\"body\":{\"input\":\"synthetic one\"}}\n" +
	"{\"custom_id\":\"two\",\"method\":\"POST\",\"url\":\"/v1/responses\",\"body\":{\"input\":\"synthetic two\"}}\n"

const simpleBatchOutput = "{\"custom_id\":\"one\",\"response\":{\"body\":{\"output_text\":\"synthetic result\"}}}\n"

type simpleBatchServer struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []string
	body     gjson.Result
	polls    int
}

func newSimpleBatchServer(t *testing.T, payload, endpoint, status string) *simpleBatchServer {
	t.Helper()
	f := &simpleBatchServer{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer sk-fake-readable-test" || r.Header.Get("OpenAI-Project") != "proj_synthetic" || r.Header.Get("X-Synthetic") != "preserved" {
			t.Error("request lost authentication, project, or literal header")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			data, err := io.ReadAll(r.Body)
			if err != nil || len(data) != 0 || r.URL.RawQuery != "" {
				t.Error("creation fields leaked into a follow-up GET")
			}
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil || string(data) != payload || r.FormValue("purpose") != "batch" || len(r.MultipartForm.Value) != 1 {
				t.Error("synthetic input upload changed")
			}
			io.WriteString(w, `{"id":"file_simple_uploaded","purpose":"batch"}`)
		case "POST /batches":
			data, err := io.ReadAll(r.Body)
			if err != nil || !gjson.ValidBytes(data) {
				t.Error("invalid create request")
			}
			f.body = gjson.ParseBytes(data)
			for name, value := range map[string]string{"input_file_id": "file_simple_uploaded", "endpoint": endpoint, "completion_window": "24h"} {
				if f.body.Get(name).String() != value {
					t.Errorf("create field %s changed", name)
				}
			}
			io.WriteString(w, `{"id":"batch_simple_created","status":"validating","input_file_id":"file_simple_uploaded"}`)
		case "GET /batches/batch_simple_created":
			f.polls++
			current := status
			if current == "progress_then_completed" {
				current = "completed"
				if f.polls == 1 {
					current = "in_progress"
				}
			}
			counts := `{"total":2,"completed":2,"failed":0}`
			if current != "completed" {
				counts = `{"total":2,"completed":0,"failed":0}`
			}
			errorFile := ""
			if current == "completed_partial" {
				current = "completed"
				counts = `{"total":2,"completed":1,"failed":1}`
				errorFile = "file_simple_error"
			}
			io.WriteString(w, `{"id":"batch_simple_created","status":"`+current+`","input_file_id":"file_simple_uploaded","output_file_id":"file_simple_output","error_file_id":"`+errorFile+`","request_counts":`+counts+`}`)
		case "GET /files/file_simple_output/content":
			io.WriteString(w, simpleBatchOutput)
		case "GET /files/file_simple_error/content":
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"message":"You have insufficient permissions. Missing scopes: api.files.read.","type":"invalid_request_error","code":null}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func simpleBatchPath(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input with spaces.jsonl")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runSimpleBatchCommand(t *testing.T, fixture *simpleBatchServer, stdin *os.File, flags ...string) mainDispatchResult {
	t.Helper()
	args := []string{"openai", "--format", "json", "--format-error", "json", "--project", "proj_synthetic", "--header", "X-Synthetic: preserved", "batches", "submit"}
	return runMainDispatchWithStdin(t, "bash", []string{"OPENAI_BASE_URL=" + fixture.server.URL, "OPENAI_API_KEY=sk-fake-readable-test", "FORCE_COLOR=0"}, stdin, append(args, flags...)...)
}

func TestMainBatchSubmitSimpleDefaults(t *testing.T) {
	for _, source := range []string{"positional", "flag"} {
		t.Run(source, func(t *testing.T) {
			fixture := newSimpleBatchServer(t, simpleBatchInput, "/v1/responses", "completed")
			path := simpleBatchPath(t, simpleBatchInput)
			args := []string{path}
			if source == "flag" {
				args = []string{"--file", path}
			}
			got := runSimpleBatchCommand(t, fixture, nil, args...)
			if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) || gjson.Get(got.stdout, "id").String() != "batch_simple_created" {
				t.Fatalf("short submit failed: %+v", got)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if !reflect.DeepEqual(fixture.requests, []string{"POST /files", "POST /batches"}) {
				t.Fatalf("unexpected mutations: %v", fixture.requests)
			}
		})
	}
}

func TestMainBatchSubmitSimpleExplicitInput(t *testing.T) {
	for _, mode := range []string{"flags", "json", "yaml"} {
		t.Run(mode, func(t *testing.T) {
			// Explicit endpoint preserves the existing upload path without JSONL inference.
			const payload = "synthetic input that requires explicit endpoint\n"
			fixture := newSimpleBatchServer(t, payload, "/v1/embeddings", "completed")
			args := []string{simpleBatchPath(t, payload), "--wait"}
			var stdin *os.File
			if mode == "flags" {
				args = append(args, "--endpoint", "/v1/embeddings")
			} else {
				body := `{"endpoint":"/v1/embeddings","completion_window":"24h","metadata":{"case":"synthetic"},"future":{"exact":9007199254740993}}`
				if mode == "yaml" {
					body = "endpoint: /v1/embeddings\ncompletion_window: 24h\nmetadata:\n  case: synthetic\nfuture:\n  exact: 9007199254740993\n"
				}
				var err error
				stdin, err = os.Open(simpleBatchPath(t, body))
				if err != nil {
					t.Fatal(err)
				}
				defer stdin.Close()
			}
			got := runSimpleBatchCommand(t, fixture, stdin, args...)
			if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) || gjson.Get(got.stdout, "status").String() != "completed" {
				t.Fatalf("explicit submit failed: %+v", got)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if mode != "flags" && (fixture.body.Get("future.exact").Raw != "9007199254740993" || fixture.body.Get("metadata.case").String() != "synthetic") {
				t.Fatal("explicit input fields changed")
			}
			if !reflect.DeepEqual(fixture.requests, []string{"POST /files", "POST /batches", "GET /batches/batch_simple_created"}) {
				t.Fatalf("unexpected explicit submit/wait sequence: %v", fixture.requests)
			}
		})
	}
}

func TestMainBatchSubmitSimplePreflight(t *testing.T) {
	path := simpleBatchPath(t, simpleBatchInput)
	fileDestination := simpleBatchPath(t, "existing file")
	conflictingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(conflictingDirectory, "output.jsonl"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"both file forms", []string{path, "--file", path}},
		{"two paths", []string{path, path}},
		{"no file", nil},
		{"output requires wait", []string{path, "--output-dir", t.TempDir()}},
		{"explicit false wait", []string{path, "--wait=false", "--output-dir", t.TempDir()}},
		{"poll requires wait", []string{path, "--poll-interval", "1s"}},
		{"timeout requires wait", []string{path, "--wait-timeout", "1s"}},
		{"zero poll", []string{path, "--wait", "--poll-interval", "0"}},
		{"negative poll", []string{path, "--wait", "--poll-interval", "-1s"}},
		{"negative timeout", []string{path, "--wait", "--wait-timeout", "-1s"}},
		{"malformed timeout", []string{path, "--wait", "--wait-timeout", "invalid"}},
		{"output is file", []string{path, "--wait", "--output-dir", fileDestination}},
		{"output exists", []string{path, "--wait", "--output-dir", conflictingDirectory}},
		{"ambiguous endpoint", []string{simpleBatchPath(t, simpleBatchInput+`{"url":"/v1/embeddings"}`+"\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runReadableCommand(t, server, append([]string{"--format", "json", "--format-error", "json", "batches", "submit"}, tc.args...)...)
			if got.code == 0 || got.stdout != "" || !json.Valid([]byte(got.stderr)) {
				t.Fatalf("invalid submit did not fail cleanly: %+v", got)
			}
			if requests.Load() != 0 {
				t.Fatal("preflight failure sent an HTTP request")
			}
		})
	}
	if data, err := os.ReadFile(filepath.Join(conflictingDirectory, "output.jsonl")); err != nil || string(data) != "keep" {
		t.Fatal("preflight changed an existing result file")
	}
}

func TestMainBatchSubmitSimpleWait(t *testing.T) {
	fixture := newSimpleBatchServer(t, simpleBatchInput, "/v1/responses", "progress_then_completed")
	destination := filepath.Join(t.TempDir(), "saved results")
	got := runSimpleBatchCommand(t, fixture, nil, simpleBatchPath(t, simpleBatchInput), "--wait", "--output-dir", destination, "--metadata", `{"case":"wait"}`, "--poll-interval", "1ms")
	if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) || gjson.Get(got.stdout, "status").String() != "completed" {
		t.Fatalf("combined wait failed or emitted multiple objects: %+v", got)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "output.jsonl")); err != nil || string(data) != simpleBatchOutput {
		t.Fatal("combined wait did not save exact output bytes")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !reflect.DeepEqual(fixture.requests, []string{"POST /files", "POST /batches", "GET /batches/batch_simple_created", "GET /batches/batch_simple_created", "GET /files/file_simple_output/content"}) {
		t.Fatalf("unexpected submit/wait sequence: %v", fixture.requests)
	}
}

func TestMainBatchSubmitSimpleWaitFailure(t *testing.T) {
	for _, status := range []string{"in_progress", "failed"} {
		t.Run(status, func(t *testing.T) {
			fixture := newSimpleBatchServer(t, simpleBatchInput, "/v1/responses", status)
			args := []string{simpleBatchPath(t, simpleBatchInput), "--wait", "--poll-interval", "1h"}
			wantCode := 1
			if status == "in_progress" {
				args = append(args, "--wait-timeout", "250ms")
				wantCode = 124
			}
			got := runSimpleBatchCommand(t, fixture, nil, args...)
			if got.code != wantCode || !json.Valid([]byte(got.stderr)) || !strings.Contains(got.stderr, "batch_simple_created") || !strings.Contains(got.stderr, "Do not repeat submit") {
				t.Fatalf("wait failure lost status or recovery: %+v", got)
			}
			if status == "in_progress" && got.stdout != "" {
				t.Fatalf("timeout printed an intermediate creation object: %s", got.stdout)
			}
			if status == "failed" && (!json.Valid([]byte(got.stdout)) || gjson.Get(got.stdout, "status").String() != "failed") {
				t.Fatalf("terminal failure lost its final object: %s", got.stdout)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if !reflect.DeepEqual(fixture.requests, []string{"POST /files", "POST /batches", "GET /batches/batch_simple_created"}) {
				t.Fatalf("wait failure repeated a mutation: %v", fixture.requests)
			}
		})
	}
}

func TestMainBatchSubmitSimplePartialDownload(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			fixture := newSimpleBatchServer(t, simpleBatchInput, "/v1/responses", "completed_partial")
			destination := filepath.Join(t.TempDir(), "results")
			got := runReadableCommand(t, fixture.server, "--format", "json", "--format-error", format, "--project", "proj_synthetic", "--header", "X-Synthetic: preserved", "batches", "submit", simpleBatchPath(t, simpleBatchInput), "--wait", "--output-dir", destination)
			if got.code == 0 || !json.Valid([]byte(got.stdout)) || gjson.Get(got.stdout, "status").String() != "completed" {
				t.Fatalf("partial download lost terminal batch or failure status: %+v", got)
			}
			if format == "json" && !json.Valid([]byte(got.stderr)) {
				t.Fatalf("invalid JSON error: %s", got.stderr)
			}
			for _, want := range []string{"batch_simple_created", "Could not save error.jsonl", "Any completed files remain saved", "Do not repeat submit"} {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("partial download omitted %q: %s", want, got.stderr)
				}
			}
			if data, err := os.ReadFile(filepath.Join(destination, "output.jsonl")); err != nil || string(data) != simpleBatchOutput {
				t.Fatal("partial download did not preserve the completed output file")
			}
			if _, err := os.Stat(filepath.Join(destination, "error.jsonl")); !os.IsNotExist(err) {
				t.Fatal("partial download published an error file after HTTP 403")
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if !reflect.DeepEqual(fixture.requests, []string{"POST /files", "POST /batches", "GET /batches/batch_simple_created", "GET /files/file_simple_output/content", "GET /files/file_simple_error/content"}) {
				t.Fatalf("unexpected partial-download requests: %v", fixture.requests)
			}
		})
	}
}

func TestMainBatchSubmitSimpleMissingPrivateFile(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, explicit := range []bool{false, true} {
			name := format + "/inferred"
			if explicit {
				name = format + "/explicit"
			}
			t.Run(name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.WriteHeader(500)
				}))
				defer server.Close()
				path := filepath.Join(t.TempDir(), "private-customer-records.jsonl")
				args := []string{"--format-error", format, "batches", "submit", path}
				if explicit {
					args = append(args, "--endpoint", "/v1/responses")
				}
				got := runReadableCommand(t, server, args...)
				if got.code == 0 || got.stdout != "" || got.stderr == "" || requests.Load() != 0 {
					t.Fatalf("missing input did not fail before HTTP: %+v", got)
				}
				if strings.Contains(got.stderr, filepath.Dir(path)) || strings.Contains(got.stderr, filepath.Base(path)) {
					t.Fatalf("missing input error exposed the private path: %s", got.stderr)
				}
				if format == "json" && !json.Valid([]byte(got.stderr)) {
					t.Fatalf("missing input error is not JSON: %s", got.stderr)
				}
			})
		}
	}
}
