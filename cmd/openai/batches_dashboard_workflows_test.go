package main

import (
	"io"
	"net/http"
	"net/http/httptest"

	"strings"

	"testing"

	"github.com/tidwall/gjson"
)

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
