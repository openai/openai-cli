package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"
)

func TestMainDispatchBatchInputsSearchAndSelectionGuards(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/files" || r.URL.Query().Get("purpose") != "batch" || r.URL.Query().Get("order") != "desc" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			io.WriteString(w, `{"data":[{"id":"file_other","object":"file","filename":"other.jsonl","purpose":"batch"}],"has_more":true}`)
			return
		}
		if r.URL.Query().Get("after") != "file_other" {
			t.Errorf("unexpected cursor: %s", r.URL)
		}
		io.WriteString(w, `{"data":[{"id":"file_requests","object":"file","filename":"資料-ReQuEsTs[1].jsonl","purpose":"batch","bytes":9007199254740993,"future":null}],"has_more":false}`)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "--format", "JsOn", "batches", "inputs", "--search", "requests[1]", "--max-items", "-1")
	value := gjson.Parse(got.stdout)
	if got.code != 0 || got.stderr != "" || requests.Load() != 2 || !value.IsArray() || len(value.Array()) != 1 || value.Get("0.id").Str != "file_requests" || value.Get("0.bytes").Raw != "9007199254740993" || !value.Get("0.future").Exists() {
		t.Fatalf("search lost matching metadata: %+v requests=%d", got, requests.Load())
	}
	for _, options := range [][]string{nil, {"--format", "json"}, {"--format", "json", "--format-error", "text"}, {"--transform-error", "message"}} {
		got := runReadableCommand(t, server, append(slices.Clone(options), "batches", "inputs", "--select")...)
		if got.code == 0 || got.stdout != "" || got.stderr == "" || requests.Load() != 2 {
			t.Fatalf("selection must reject piped stdin/stderr before HTTP: %+v requests=%d", got, requests.Load())
		}
	}
}

func TestMainDispatchBatchResultsMatchByExactIDAndPreserveModes(t *testing.T) {
	input := "{\"custom_id\":\"Case\",\"body\":{\"label\":\"upper\"}}\n{\"custom_id\":\"case\",\"body\":{\"label\":\"lower\"}}\n{\"custom_id\":\"pending\",\"body\":{\"label\":\"pending\"}}\n"
	output := "{\"custom_id\":\"case\",\"response\":{\"body\":{\"label\":\"lower\"}}}\n{\"custom_id\":\"Case\",\"response\":{\"body\":{\"label\":\"upper\",\"tokens\":9007199254740993}}}\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("results mutated API: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/batches/batch_synthetic":
			io.WriteString(w, `{"id":"batch_synthetic","input_file_id":"file_input","output_file_id":"file_output"}`)
		case "/files/file_input/content":
			io.WriteString(w, input)
		case "/files/file_output/content":
			io.WriteString(w, output)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, format := range []string{"JsOn", "JsOnL", "RaW", "YaMl", "text", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--format", format, "batches", "results", "batch_synthetic", "--match-input")
			if got.code != 0 || format != "explore" && got.stderr != "" {
				t.Fatalf("results mode failed: %+v", got)
			}
			if format == "explore" && !strings.Contains(got.stderr, "falling back to 'json'") {
				t.Fatalf("missing explicit browser fallback: %+v", got)
			}
			if format == "YaMl" || format == "text" || format == "pretty" {
				for _, expected := range []string{"Case", "case", "pending", "missing_result", "9007199254740993"} {
					if !strings.Contains(got.stdout, expected) {
						t.Errorf("mode %s lost %s: %q", format, expected, got.stdout)
					}
				}
				return
			}
			if !gjson.Valid(got.stdout) {
				t.Fatalf("invalid JSON: %+v", got)
			}
			rows := gjson.Get(got.stdout, "data").Array()
			if len(rows) != 3 {
				t.Fatalf("unexpected match rows: %q", got.stdout)
			}
			for _, row := range rows {
				id := row.Get("custom_id").Str
				if id == "pending" {
					if row.Get("match").Str != "missing_result" || len(row.Get("results").Array()) != 0 {
						t.Errorf("pending input lost: %s", row.Raw)
					}
					continue
				}
				if row.Get("match").Str != "matched" || row.Get("inputs.0.custom_id").Str != id || row.Get("results.0.custom_id").Str != id || row.Get("inputs.0.body.label").Str != row.Get("results.0.response.body.label").Str {
					t.Errorf("incorrect pairing: %s", row.Raw)
				}
			}
		})
	}
	got := runReadableCommand(t, server, "--transform", "data.0.custom_id", "--raw-output", "batches", "results", "batch_synthetic", "--custom-id", "Case", "--match-input")
	if got.code != 0 || got.stderr != "" || got.stdout != "Case\n" {
		t.Fatalf("exact-ID extraction changed: %+v", got)
	}
}

func TestMainDispatchBatchWatchSnapshotsAndOrdinaryList(t *testing.T) {
	var mu sync.Mutex
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/batches" || r.URL.Query().Get("limit") != "1" {
			t.Errorf("unexpected list request: %s %s", r.Method, r.URL)
		}
		cursor := r.URL.Query().Get("after")
		mu.Lock()
		cursors = append(cursors, cursor)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if cursor == "seed" {
			io.WriteString(w, "{\"data\":[{\n\"id\":\"batch_a\",\"object\":\"batch\",\"status\":\"completed\",\"created_at\":1700000000}],\"has_more\":true}")
			return
		}
		io.WriteString(w, `{"data":[{"id":"batch_b","object":"batch","status":"validating","created_at":1700000001}],"has_more":false}`)
	}))
	defer server.Close()
	base := []string{"batches", "list", "--after", "seed", "--limit", "1", "--max-items", "-1"}
	watch := append(slices.Clone(base), "--watch", "--watch-count", "2", "--poll-interval", "1ns")
	got := runReadableCommand(t, server, append([]string{"--format", "JsOnL"}, watch...)...)
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("JSONL watch failed: %+v", got)
	}
	lines := strings.Split(strings.TrimSpace(got.stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("watch is not one JSONL envelope per snapshot: %q", got.stdout)
	}
	for i, line := range lines {
		value := gjson.Parse(line)
		if !gjson.Valid(line) || value.Get("object").Str != "batch.list_snapshot" || value.Get("sequence").Int() != int64(i+1) || !value.Get("complete").Bool() || len(value.Get("data").Array()) != 2 {
			t.Errorf("invalid snapshot %d: %s", i, line)
		}
	}
	mu.Lock()
	actual := slices.Clone(cursors)
	cursors = nil
	mu.Unlock()
	if !slices.Equal(actual, []string{"seed", "batch_a", "seed", "batch_a"}) {
		t.Fatalf("watch did not restart cursor: %q", actual)
	}
	got = runReadableCommand(t, server, watch...)
	if got.code != 0 || got.stderr != "" || strings.Count(got.stdout, "Batch snapshot ") != 2 || strings.Count(got.stdout, "ID: batch_a\n") != 2 || !strings.Contains(got.stdout, "Created at: 2023-11-14T22:13:20Z") || strings.Contains(got.stdout+got.stderr, "Full data: --format json.") {
		t.Fatalf("readable snapshots or hint policy failed: %+v", got)
	}
	got = runReadableCommand(t, server, append([]string{"--format", "json"}, base...)...)
	if got.code != 0 || got.stderr != "" || !gjson.Parse(got.stdout).IsArray() || strings.Contains(got.stdout, "batch.list_snapshot") {
		t.Fatalf("ordinary list changed: %+v", got)
	}
	mu.Lock()
	before := len(cursors)
	mu.Unlock()
	for _, flags := range [][]string{{"--format", "json"}, {"--format", "yaml"}, {"--transform", "id"}, {"--raw-output"}} {
		got := runReadableCommand(t, server, append(slices.Clone(flags), watch...)...)
		mu.Lock()
		after := len(cursors)
		mu.Unlock()
		if got.code == 0 || got.stdout != "" || after != before {
			t.Fatalf("watch preflight failed for %v: %+v requests=%d/%d", flags, got, after, before)
		}
	}
}

func TestMainDispatchBatchWatchPartialFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			fmt.Fprint(w, `{"data":[{"id":"batch_kept","object":"batch","status":"validating"}],"has_more":true}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"synthetic second-page failure","type":"invalid_request_error"}}`)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "--format", "jsonl", "batches", "list", "--watch", "--watch-count", "2", "--max-items", "-1")
	if got.code == 0 || !gjson.Valid(got.stdout) || gjson.Get(got.stdout, "complete").Bool() || gjson.Get(got.stdout, "data.0.id").Str != "batch_kept" || strings.Count(got.stdout, "\n") != 1 {
		t.Fatalf("partial snapshot or failure lost: %+v", got)
	}
}
