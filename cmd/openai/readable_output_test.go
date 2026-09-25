package main

import (
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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const readableModelResponse = `{"id":"model_synthetic","object":"model","created":17,"owned_by":"synthetic","extra":{"note":"full API value"}}`

func runReadableCommand(t *testing.T, server *httptest.Server, args ...string) mainDispatchResult {
	t.Helper()
	return runMainDispatchWithEnv(t, "bash", []string{
		"OPENAI_BASE_URL=" + server.URL,
		"OPENAI_API_KEY=sk-fake-readable-test",
		"FORCE_COLOR=0",
	}, append([]string{"openai"}, args...)...)
}

func TestMainReadableDefaultAndExplicitText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models/model_synthetic" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, readableModelResponse)
	}))
	defer server.Close()

	for _, format := range []string{"", "auto", "text", "TeXt"} {
		t.Run("format="+format, func(t *testing.T) {
			args := []string{"models", "retrieve", "model_synthetic"}
			if format != "" {
				args = append([]string{"--format", format}, args...)
			}
			// The process helper connects stdout to a pipe: auto must still be readable.
			got := runReadableCommand(t, server, args...)
			want := "ID: model_synthetic\nObject: model\nCreated: 17\nOwned by: synthetic\nExtra:\n  Note: full API value\n"
			if got.code != 0 || got.stderr != "" || got.stdout != want {
				t.Fatalf("got %+v; want readable stdout %q", got, want)
			}
		})
	}
}

func TestMainReadablePreservesExplicitFormatsAndExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, readableModelResponse)
	}))
	defer server.Close()

	for _, format := range []string{"json", "JSON", "JsOnL", "raw", "RAW", "yaml", "YAML", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--format", format, "models", "retrieve", "model_synthetic")
			if got.code != 0 || format != "explore" && got.stderr != "" {
				t.Fatalf("unexpected process result: %+v", got)
			}
			switch strings.ToLower(format) {
			case "json", "jsonl", "raw", "explore":
				var want, actual any
				if err := json.Unmarshal([]byte(readableModelResponse), &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(got.stdout), &actual); err != nil || !reflect.DeepEqual(actual, want) {
					t.Fatalf("API JSON changed: stdout=%q, error=%v", got.stdout, err)
				}
				if strings.EqualFold(format, "raw") && got.stdout != readableModelResponse+"\n" {
					t.Fatalf("raw bytes changed: %q", got.stdout)
				}
				if strings.EqualFold(format, "jsonl") && strings.Count(got.stdout, "\n") != 1 {
					t.Fatalf("jsonl must contain one line: %q", got.stdout)
				}
			default:
				for _, value := range []string{"model_synthetic", "synthetic", "full API value"} {
					if !strings.Contains(got.stdout, value) {
						t.Fatalf("explicit %s lost %q: %q", format, value, got.stdout)
					}
				}
			}
			if format == "explore" && !strings.Contains(got.stderr, "falling back to 'json'") {
				t.Fatalf("missing nonterminal explore warning: %q", got.stderr)
			}
		})
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"transform", []string{"--transform", "extra.note"}, "\"full API value\"\n"},
		{"explicit auto transform", []string{"--format", "auto", "--transform", "extra.note"}, "\"full API value\"\n"},
		{"raw string", []string{"--transform", "extra.note", "--raw-output"}, "full API value\n"},
		{"explicit text transform", []string{"--format", "text", "--transform", "extra.note"}, "full API value\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(tc.args, "models", "retrieve", "model_synthetic")
			if got := runReadableCommand(t, server, args...); got.code != 0 || got.stderr != "" || got.stdout != tc.want {
				t.Fatalf("got %+v; want stdout %q", got, tc.want)
			}
		})
	}
}

func TestMainReadableListPreservesItemsAndRawPage(t *testing.T) {
	const item = `{"id":"file_same","object":"file","bytes":5,"created_at":17,"filename":"synthetic.txt","purpose":"assistants"}`
	const page = `{"object":"list","data":[` + item + `,` + item + `],"has_more":true,"last_id":"file_same","first_id":"file_same"}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/files" || r.URL.Query().Get("after") != "" {
			t.Errorf("unexpected request or extra page: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, page)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name         string
		args         []string
		count, calls int
		rawPage      bool
	}{
		{"default items", []string{"files", "list", "--max-items", "2"}, 2, 1, false},
		{"one item", []string{"files", "list", "--max-items", "1"}, 1, 1, false},
		// The SDK fetches the first page when constructing its iterator, even at zero.
		{"zero items", []string{"files", "list", "--max-items", "0"}, 0, 1, false},
		{"raw page", []string{"--format", "raw", "files", "list"}, 0, 1, true},
		{"mixed-case raw page", []string{"--format", "RaW", "files", "list"}, 0, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests.Store(0)
			got := runReadableCommand(t, server, tc.args...)
			if got.code != 0 || got.stderr != "" || int(requests.Load()) != tc.calls {
				t.Fatalf("result=%+v requests=%d; want %d", got, requests.Load(), tc.calls)
			}
			if tc.rawPage {
				if got.stdout != page+"\n" {
					t.Fatalf("raw list envelope changed: %q", got.stdout)
				}
			} else if strings.Count(got.stdout, "ID: file_same\n") != tc.count || tc.count == 0 && got.stdout != "" {
				t.Fatalf("list record count changed: %q", got.stdout)
			}
		})
	}
}

func TestMainReadableStreamAssemblesIndividualEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\ndata: "+
			`{"type":"response.output_text.delta","delta":"Synthetic answer","output_index":0,"content_index":0,"sequence_number":0}`+"\n\n")
		io.WriteString(w, "event: response.output_text.done\ndata: "+
			`{"type":"response.output_text.done","text":"Synthetic answer","output_index":0,"content_index":0,"sequence_number":1}`+"\n\n")
		io.WriteString(w, "event: response.completed\ndata: "+
			`{"type":"response.completed","response":{"status":"completed","output":[]}}`+"\n\n")
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "responses", "create", "--model", "fake-model", "--input", "synthetic input", "--stream=true")
	if got.code != 0 || got.stderr != "" || got.stdout != "Synthetic answer\n" {
		t.Fatalf("streamed text duplicated or lost: %+v", got)
	}
}

func TestMainReadableRawOutputStreamsBeforeNextEvent(t *testing.T) {
	for _, tc := range []struct {
		name, transform, first, second string
	}{
		{"objects", "", "Type: response.output_text.delta\nDelta: first\\u001b\nSequence number: 0\n", "\nType: response.output_text.delta\nDelta: second\nSequence number: 1\n"},
		{"raw strings", "delta", "first\x1b\n", "second\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "event: response.output_text.delta\ndata: "+
					`{"type":"response.output_text.delta","delta":"first\u001b","sequence_number":0}`+"\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				io.WriteString(w, "event: response.output_text.delta\ndata: "+
					`{"type":"response.output_text.delta","delta":"second","sequence_number":1}`+"\n\n")
				io.WriteString(w, "event: response.completed\ndata: "+
					`{"type":"response.completed","response":{"status":"completed","output":[]}}`+"\n\n")
			}))
			defer server.Close()
			// Always unblock the synthetic source, including when an assertion fails.
			defer close(release)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			args := []string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "--format", "text", "--raw-output"}
			if tc.transform != "" {
				args = append(args, "--transform", tc.transform)
			}
			args = append(args, "responses", "create", "--model", "fake-model", "--input", "synthetic input", "--stream=true")
			child := exec.CommandContext(ctx, binary, args...)
			child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-readable-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
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
			first := make([]byte, len(tc.first))
			read := make(chan error, 1)
			go func() { _, err := io.ReadFull(stdout, first); read <- err }()
			select {
			case err := <-read:
				if err != nil {
					t.Fatalf("first event did not reach stdout: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("first event was buffered while the next event was withheld")
			}
			if string(first) != tc.first {
				t.Fatalf("first output = %q, want %q", first, tc.first)
			}
			release <- struct{}{}
			rest, err := io.ReadAll(stdout)
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil || stderr.Len() != 0 {
				t.Fatalf("process failed: %v; stderr=%q", err, stderr.String())
			}
			want := tc.second + "\nType: response.completed\nResponse:\n  Status: completed\n  Output: (empty list)\n"
			if string(rest) != want {
				t.Fatalf("remaining output = %q, want %q", rest, want)
			}
		})
	}
}

func TestMainReadableLeavesBinaryBytesUntouched(t *testing.T) {
	payload := []byte{0, 0xff, '\n', '\r', 0x1b, '[', 'm', 0x80}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/files/file_synthetic/content" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(payload)
	}))
	defer server.Close()
	for _, prefix := range [][]string{nil, {"--format", "text"}, {"--format", "json"}} {
		args := append(prefix, "files", "content", "file_synthetic", "--output", "-")
		got := runReadableCommand(t, server, args...)
		if got.code != 0 || got.stderr != "" || got.stdout != string(payload) {
			t.Fatalf("binary output changed for %q: %+v", args, got)
		}
	}
}

func TestMainReadableKeepsAPIErrorOnStderr(t *testing.T) {
	const apiError = `{"message":"synthetic invalid request","type":"invalid_request_error","code":"synthetic_code"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":`+apiError+`}`)
	}))
	defer server.Close()
	for _, prefix := range [][]string{nil, {"--format", "text"}, {"--format-error", "jsonl"}} {
		args := append(prefix, "models", "retrieve", "model_synthetic")
		got := runReadableCommand(t, server, args...)
		wantPrefix := fmt.Sprintf("GET %q: 400 Bad Request\n", server.URL+"/models/model_synthetic")
		if got.code != 1 || got.stdout != "" || !strings.HasPrefix(got.stderr, wantPrefix) {
			t.Fatalf("error routing or prefix changed: %+v", got)
		}
		var want, actual any
		if err := json.Unmarshal([]byte(apiError), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(got.stderr, wantPrefix)), &actual); err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatalf("API error JSON changed: stderr=%q error=%v", got.stderr, err)
		}
	}
}
