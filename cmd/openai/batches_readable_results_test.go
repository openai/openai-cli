package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

const batchReadableInput = `{"custom_id":"request-1","method":"POST","url":"/v1/responses","body":{"model":"synthetic-model","instructions":"Return only the label.","input":"The delivery arrived early.","max_output_tokens":32}}`
const batchReadableResult = `{"custom_id":"request-1","response":{"status_code":200,"request_id":"req_synthetic","body":{"id":"resp_synthetic","object":"response","status":"completed","temperature":1,"top_p":1,"store":false,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"positive","annotations":[]}]}],"usage":{"input_tokens":31,"output_tokens":5,"total_tokens":36}}},"error":null}`

func batchReadableServer(t *testing.T, input, output string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("result inspection attempted mutation: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/batches/batch_readable":
			_, _ = io.WriteString(w, `{"id":"batch_readable","status":"completed","input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error","request_counts":{"total":9,"completed":8,"failed":1}}`)
		case "/files/file_input/content":
			_, _ = io.WriteString(w, input+"\n")
		case "/files/file_output/content", "/files/file_error/content":
			_, _ = io.WriteString(w, output+"\n")
		default:
			t.Errorf("unexpected result request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestMainBatchReadableResultsShowRequestedContent(t *testing.T) {
	// Mirror the observed API shape with synthetic values. Extra routine fields
	// must not make the common result revert to the complete configuration tree.
	record := strings.Replace(batchReadableResult, `"temperature":1`, `"access_programs":null,"billing":{"payer":"developer"},"moderation":null,"tool_usage":{"image_gen":{"input_tokens":0,"input_tokens_details":{"image_tokens":0,"text_tokens":0},"output_tokens":0,"output_tokens_details":{"image_tokens":0,"text_tokens":0},"total_tokens":0},"web_search":{"num_requests":0}},"temperature":1`, 1)
	record = strings.Replace(record, `"role":"assistant"`, `"role":"assistant","phase":"final_answer"`, 1)
	server := batchReadableServer(t, batchReadableInput, record)
	for _, flags := range [][]string{nil, {"--format", "text"}, {"--quiet"}} {
		got := runReadableCommand(t, server, append(flags, "batches", "results", "batch_readable", "--custom-id", "request-1", "--match-input")...)
		if got.code != 0 {
			t.Fatalf("readable result failed: %+v", got)
		}
		for _, want := range []string{"batch_readable", "request-1", "completed", "matched", "Return only the label.", "The delivery arrived early.", "positive", "31", "5"} {
			if !strings.Contains(got.stdout, want) {
				t.Errorf("readable result lost %q: %s", want, got.stdout)
			}
		}
		for _, clutter := range []string{"Temperature:", "Top p:", "HTTP 200", "Status code: 200"} {
			if strings.Contains(got.stdout, clutter) {
				t.Errorf("readable result includes routine detail %q: %s", clutter, got.stdout)
			}
		}
		if strings.Count(got.stdout, "batch_readable") != 1 || strings.Count(got.stdout+got.stderr, "Full data:") > 1 {
			t.Errorf("repeated result context: %+v", got)
		}
	}
}

func TestMainBatchRawTextLargeFieldAndRecord(t *testing.T) {
	// The renderer spills this label while the producer starts spilling the next record.
	label := strings.Repeat("l", 8192)
	value := strings.Repeat("v", 300<<10)
	first := `{"custom_id":"first","` + label + `":"label-value"}`
	second := `{"custom_id":"second","payload":"` + value + `"}`
	// A small compressed transfer lets both workers process the records without
	// waiting on a later network packet.
	var body bytes.Buffer
	zipped := gzip.NewWriter(&body)
	if _, err := io.WriteString(zipped, first+"\n"+second+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/batches/batch_readable":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"batch_readable","input_file_id":"file_input","output_file_id":"file_output"}`)
		case "/files/file_input/content":
			io.WriteString(w, "{\"custom_id\":\"first\"}\n{\"custom_id\":\"second\"}\n")
		case "/files/file_output/content":
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", fmt.Sprint(body.Len()))
			w.Write(body.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, mode := range []string{"streamed", "matched"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			args := []string{"batches", "results", "batch_readable", "--format", "text", "--raw-output"}
			if mode == "matched" {
				args = append(args, "--match-input")
			}
			child := batchesWorkflowProcess(t, t.Context(), server, args...)
			child.Env = append(child.Env, "TMPDIR="+dir, "TMP="+dir, "TEMP="+dir)
			var stdout, stderr strings.Builder
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Run(); err != nil {
				t.Fatalf("raw text failed: %v; stderr: %s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), "L"+label[1:]+": label-value") || !strings.Contains(stdout.String(), value) {
				t.Fatal("raw text lost the complete label or record")
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
				t.Fatalf("scratch cleanup: %v; files: %v", err, entries)
			}
		})
	}
}

func TestMainBatchReadableResultsPreserveContentKinds(t *testing.T) {
	tests := []struct {
		name, body string
		want       []string
	}{
		{"multiple_messages", `{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"FIRST_MESSAGE"}]},{"type":"message","content":[{"type":"output_text","text":"SECOND_MESSAGE"}]}]}`, []string{"FIRST_MESSAGE", "SECOND_MESSAGE"}},
		{"refusal", `{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"SYNTHETIC_REFUSAL"}]}]}`, []string{"refus", "SYNTHETIC_REFUSAL"}},
		{"incomplete", `{"object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"PARTIAL_CONTENT"}]}]}`, []string{"incomplete", "max_output_tokens", "PARTIAL_CONTENT"}},
		{"tool_call", `{"object":"response","status":"completed","output":[{"type":"function_call","name":"synthetic_lookup","call_id":"call_example","arguments":"{\"city\":\"SYNTHETIC_CITY\"}"}]}`, []string{"synthetic_lookup", "call_example", "SYNTHETIC_CITY"}},
		{"chat_choices", `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"CHOICE_ONE"},"finish_reason":"stop"},{"index":1,"message":{"role":"assistant","content":"CHOICE_TWO"},"finish_reason":"length"}]}`, []string{"CHOICE_ONE", "CHOICE_TWO", "length"}},
		{"unknown_content", `{"object":"response","status":"completed","output":[{"type":"future_output","payload":"UNFAMILIAR_CONTENT","count":9007199254740993}],"future_field":"FUTURE_FIELD"}`, []string{"future_output", "UNFAMILIAR_CONTENT", "9007199254740993", "FUTURE_FIELD"}},
		{"annotations", `{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"CITED_TEXT","annotations":[{"type":"url_citation","url":"https://example.com/synthetic","title":"SYNTHETIC_CITATION"}]}]}]}`, []string{"CITED_TEXT", "https://example.com/synthetic", "SYNTHETIC_CITATION"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := fmt.Sprintf(`{"custom_id":"request-1","response":{"status_code":200,"body":%s},"error":null}`, test.body)
			server := batchReadableServer(t, batchReadableInput, record)
			got := runReadableCommand(t, server, "batches", "results", "batch_readable")
			if got.code != 0 {
				t.Fatalf("content inspection failed: %+v", got)
			}
			for _, want := range test.want {
				if !strings.Contains(strings.ToLower(got.stdout), strings.ToLower(want)) {
					t.Errorf("%s lost %q: %s", test.name, want, got.stdout)
				}
			}
		})
	}
}

func TestMainBatchReadableResultsPreserveMatchingProblems(t *testing.T) {
	input := batchReadableInput + "\n" + strings.ReplaceAll(batchReadableInput, "The delivery arrived early.", "SECOND_INPUT") + "\n" + strings.ReplaceAll(batchReadableInput, "request-1", "pending-request")
	output := batchReadableResult + "\n" + strings.ReplaceAll(batchReadableResult, "positive", "SECOND_RESULT")
	server := batchReadableServer(t, input, output)
	got := runReadableCommand(t, server, "batches", "results", "batch_readable", "--match-input")
	if got.code != 0 {
		t.Fatalf("matching inspection failed: %+v", got)
	}
	for _, want := range []string{"ambiguous", "SECOND_INPUT", "positive", "SECOND_RESULT", "pending-request", "missing_result"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("matching output lost %q: %s", want, got.stdout)
		}
	}
}

func TestMainBatchReadableResultsPreserveMachineModes(t *testing.T) {
	server := batchReadableServer(t, batchReadableInput, batchReadableResult)
	for _, format := range []string{"json", "jsonl", "raw"} {
		got := runReadableCommand(t, server, "--format", format, "batches", "results", "batch_readable", "--match-input")
		if got.code != 0 || got.stderr != "" || !gjson.Valid(got.stdout) {
			t.Fatalf("machine mode %s changed: %+v", format, got)
		}
		for path, want := range map[string]string{"data.0.results.0.response.body.temperature": "1", "data.0.results.0.response.body.store": "false", "data.0.results.0.error": "null", "data.0.inputs.0.body.max_output_tokens": "32"} {
			if gjson.Get(got.stdout, path).Raw != want {
				t.Errorf("machine mode %s lost %s: %s", format, path, got.stdout)
			}
		}
	}
	got := runReadableCommand(t, server, "--transform", "data.0.response.body.output.0.content.0.text", "--raw-output", "batches", "results", "batch_readable")
	if got.code != 0 || got.stdout != "positive\n" || got.stderr != "" {
		t.Fatalf("extraction changed: %+v", got)
	}
}

func TestMainBatchResultsRawTextPreservesCompleteRecords(t *testing.T) {
	server := batchReadableServer(t, batchReadableInput, batchReadableResult)
	for _, test := range []struct {
		name  string
		flags []string
		want  []string
	}{
		{"explicit_text", []string{"--format", "text", "--raw-output"}, []string{"Temperature: 1", "Top p: 1", "Store: false", "Error: (null)"}},
		{"error_file", []string{"--format", "text", "--raw-output", "--file", "error"}, []string{"File: error", "Temperature: 1", "Top p: 1"}},
		{"input_file", []string{"--format", "text", "--raw-output", "--file", "input"}, []string{"Max output tokens: 32", "Method: POST", "URL: /v1/responses"}},
		{"matched_input", []string{"--format", "text", "--raw-output", "--match-input"}, []string{"Match: matched", "Max output tokens: 32", "Temperature: 1", "Top p: 1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"batches", "results", "batch_readable"}, test.flags...)
			got := runReadableCommand(t, server, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("complete text output failed: %+v", got)
			}
			for _, want := range test.want {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("complete text output lost %q: %s", want, got.stdout)
				}
			}
		})
	}
	got := runReadableCommand(t, server, "batches", "results", "batch_readable", "--raw-output")
	if got.code != 0 || got.stderr != "" || !gjson.Valid(got.stdout) || gjson.Get(got.stdout, "data.0.response.body.temperature").Raw != "1" {
		t.Fatalf("automatic raw output changed its complete JSON contract: %+v", got)
	}
	got = runReadableCommand(t, server, "batches", "results", "batch_readable", "--format", "text", "--raw-output=false")
	if got.code != 0 || !strings.Contains(got.stdout, "Output: positive") || strings.Contains(got.stdout, "Temperature:") {
		t.Fatalf("explicit false bypassed readable output: %+v", got)
	}
}

func TestMainBatchReadableFailedRecordKeepsReadExitStatus(t *testing.T) {
	record := `{"custom_id":"request-1","response":{"status_code":400,"body":{"error":{"code":"invalid_request_error","message":"Unsupported synthetic parameter."}}},"error":null}`
	server := batchReadableServer(t, batchReadableInput, record)
	got := runReadableCommand(t, server, "batches", "results", "batch_readable", "--file", "error")
	if got.code != 0 || !strings.Contains(got.stdout, "400") || !strings.Contains(got.stdout, "Unsupported synthetic parameter.") {
		t.Fatalf("failed record was lost or changed successful inspection exit: %+v", got)
	}
}

func TestMainBatchReadableEmptyRefusalIsNotRefused(t *testing.T) {
	for _, refusal := range []string{`null`, `""`, `false`, `0`} {
		t.Run(refusal, func(t *testing.T) {
			record := fmt.Sprintf(`{"custom_id":"request-1","response":{"status_code":200,"body":{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"SYNTHETIC_ANSWER","refusal":%s},"finish_reason":"stop"}]}},"error":null}`, refusal)
			server := batchReadableServer(t, batchReadableInput, record)
			got := runReadableCommand(t, server, "batches", "results", "batch_readable")
			if got.code != 0 || !strings.Contains(got.stdout, "SYNTHETIC_ANSWER") || strings.Contains(got.stdout, "Content outcome: refused") {
				t.Fatalf("empty or malformed refusal incorrectly established refusal: %+v", got)
			}
			if refusal == `""` && strings.Contains(got.stdout, "Refusal: (empty string)") {
				t.Fatalf("empty refusal added misleading clutter: %s", got.stdout)
			}
		})
	}
}

func TestMainBatchReadableLargeRecordRemainsComplete(t *testing.T) {
	content := "LARGE_START" + strings.Repeat("z", 2<<20) + "LARGE_END"
	record := strings.Replace(batchReadableResult, "positive", content, 1)
	server := batchReadableServer(t, batchReadableInput, record)
	got := runReadableCommand(t, server, "batches", "results", "batch_readable")
	if got.code != 0 || !strings.Contains(got.stdout, content) {
		t.Fatalf("large selected record was lost or clipped: exit=%d stdout_bytes=%d stderr=%q", got.code, len(got.stdout), got.stderr)
	}
}
