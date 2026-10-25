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
	"testing"

	"github.com/tidwall/gjson"
)

func TestMainBatchSubmit(t *testing.T) {
	for _, inputMode := range []string{"flags", "json", "yaml"} {
		t.Run(inputMode, func(t *testing.T) {
			payload := "{\"custom_id\":\"synthetic-1\",\"body\":{\"input\":\"hello\"}}\n"
			path := filepath.Join(t.TempDir(), "input file.jsonl")
			if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer sk-fake-readable-test" || r.Header.Get("OpenAI-Project") != "proj_synthetic" || r.Header.Get("X-Synthetic") != "kept" {
					t.Error("request lost authentication, project, or custom header")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/files":
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
					if err != nil || string(data) != payload || r.FormValue("purpose") != "batch" {
						t.Error("upload changed")
					}
					if len(r.MultipartForm.Value) != 1 {
						t.Error("batch fields leaked into upload")
					}
					io.WriteString(w, `{"id":"file_synthetic_uploaded","purpose":"batch"}`)
				case "/batches":
					data, _ := io.ReadAll(r.Body)
					body := gjson.ParseBytes(data)
					for field, value := range map[string]string{"input_file_id": "file_synthetic_uploaded", "endpoint": "/v1/responses", "completion_window": "24h", "metadata.job": "synthetic", "output_expires_after.anchor": "created_at", "output_expires_after.seconds": "86400"} {
						if body.Get(field).String() != value {
							t.Errorf("%s changed: %s", field, body.Get(field))
						}
					}
					if inputMode != "flags" && body.Get("future.exact").Raw != "9007199254740993" {
						t.Error("unknown body field changed")
					}
					io.WriteString(w, `{"id":"batch_synthetic_submitted","status":"validating","created_at":1700000000,"input_file_id":"file_synthetic_uploaded"}`)
				default:
					t.Error("unexpected request")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			args := []string{"openai", "--format", "json", "--project", "proj_synthetic", "--header", "X-Synthetic: kept", "batches", "submit", "--file", path}
			var stdin *os.File
			if inputMode == "flags" {
				args = append(args, "--endpoint", "/v1/responses", "--completion-window", "24h", "--metadata", `{"job":"synthetic"}`, "--output-expires-after", `{"anchor":"created_at","seconds":86400}`)
			} else {
				body := `{"endpoint":"/v1/responses","completion_window":"24h","metadata":{"job":"synthetic"},"output_expires_after":{"anchor":"created_at","seconds":86400},"future":{"exact":9007199254740993}}`
				if inputMode == "yaml" {
					body = "endpoint: /v1/responses\ncompletion_window: 24h\nmetadata:\n  job: synthetic\noutput_expires_after:\n  anchor: created_at\n  seconds: 86400\nfuture:\n  exact: 9007199254740993\n"
				}
				p := filepath.Join(t.TempDir(), "request.txt")
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				stdin, err = os.Open(p)
				if err != nil {
					t.Fatal(err)
				}
				defer stdin.Close()
			}
			got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-readable-test"}, stdin, args...)
			if got.code != 0 || got.stderr != "" || gjson.Get(got.stdout, "id").String() != "batch_synthetic_submitted" {
				t.Fatalf("submit failed: %+v", got)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(requests, []string{"POST /files", "POST /batches"}) {
				t.Fatalf("mutations changed: %v", requests)
			}
		})
	}
}

func TestMainBatchSubmitFailureRecovery(t *testing.T) {
	for _, stage := range []string{"upload", "create"} {
		for _, format := range []string{"text", "json"} {
			t.Run(stage+"/"+format, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "synthetic.jsonl")
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var requests []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					requests = append(requests, r.URL.Path)
					io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					if stage == "create" && r.URL.Path == "/files" {
						io.WriteString(w, `{"id":"file_synthetic_saved"}`)
						return
					}
					w.WriteHeader(500)
					io.WriteString(w, `{"error":{"message":"private synthetic body","type":"server_error","code":"synthetic_failure"}}`)
				}))
				defer server.Close()
				got := runReadableCommand(t, server, "--format-error", format, "batches", "submit", "--file", path, "--endpoint", "/v1/responses", "--completion-window", "24h")
				if got.code == 0 || got.stdout != "" {
					t.Fatalf("failure was not preserved: %+v", got)
				}
				mu.Lock()
				defer mu.Unlock()
				want := []string{"/files"}
				if stage == "create" {
					want = append(want, "/batches")
				}
				if !reflect.DeepEqual(requests, want) {
					t.Fatalf("mutations retried: %v", requests)
				}
				if format == "json" {
					if !json.Valid([]byte(got.stderr)) || gjson.Get(got.stderr, "batch_submission.stage").String() != stage || !strings.Contains(got.stderr, "synthetic_failure") {
						t.Fatalf("recovery or API error lost: %s", got.stderr)
					}
					if stage == "create" && gjson.Get(got.stderr, "batch_submission.input_file_id").String() != "file_synthetic_saved" {
						t.Fatal("uploaded file ID lost")
					}
				} else if !strings.Contains(got.stderr, "before") || strings.Contains(got.stderr, "private synthetic body") {
					t.Fatalf("unsafe recovery: %s", got.stderr)
				}
			})
		}
	}
}

func TestMainBatchSubmitValidationBeforeUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid submit sent HTTP request")
		w.WriteHeader(500)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"--file", path},
		{"--file", path, "--endpoint", "/v1/responses", "--completion-window", "1h"},
		{"--file", "-", "--endpoint", "/v1/responses", "--completion-window", "24h"},
		{"--file", path, "--input-file-id", "file_conflict", "--endpoint", "/v1/responses", "--completion-window", "24h"},
	} {
		got := runReadableCommand(t, server, append([]string{"batches", "submit"}, args...)...)
		if got.code == 0 || got.stderr == "" || got.stdout != "" {
			t.Fatalf("invalid submit succeeded: %+v", got)
		}
	}
}

func TestMainBatchSubmitBodyValidationBeforeUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid body caused upload")
		w.WriteHeader(500)
	}))
	defer server.Close()
	for _, body := range []string{
		`{"endpoint":"/v1/responses","completion_window":"24h","input_file_id":"file_conflict"}`,
		`{"endpoint":"/v1/responses","completion_window":"24h","input_file_id":null}`,
		`{"endpoint":null,"completion_window":"24h"}`,
		`{"endpoint":"/v1/responses","completion_window":null}`,
		`{"endpoint":"/v1/responses","completion_window":24}`,
		`[]`, `null`, `{broken`,
	} {
		inputPath := filepath.Join(t.TempDir(), "body.json")
		if err := os.WriteFile(inputPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		stdin, err := os.Open(inputPath)
		if err != nil {
			t.Fatal(err)
		}
		got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-readable-test"}, stdin, "openai", "--format-error", "json", "batches", "submit", "--file", path)
		stdin.Close()
		if got.code == 0 || got.stdout != "" || !json.Valid([]byte(got.stderr)) {
			t.Fatalf("invalid body passed: %s; %+v", body, got)
		}
	}
}

func TestMainBatchSubmitPermissionGuidance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path != "/files" {
			t.Error("denied upload attempted batch creation")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		io.WriteString(w, `{"error":{"message":"You have insufficient permissions for this operation. Missing scopes: api.files.write. Check access.","type":"invalid_request_error"}}`)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "batches", "submit", "--file", path, "--endpoint", "/v1/responses", "--completion-window", "24h")
	if got.code != 1 || got.stdout != "" {
		t.Fatalf("denial lost: %+v", got)
	}
	for _, want := range []string{"No batch creation was attempted", "api.files.write", "Files Write", "https://platform.openai.com/api-keys"} {
		if !strings.Contains(got.stderr, want) {
			t.Fatalf("missing %q: %s", want, got.stderr)
		}
	}
}

func TestMainBatchSubmitTransportRecovery(t *testing.T) {
	for _, stage := range []string{"upload", "create"} {
		for _, format := range []string{"text", "json"} {
			t.Run(stage+"/"+format, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "synthetic.jsonl")
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var requests []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					requests = append(requests, r.URL.Path)
					io.Copy(io.Discard, r.Body)
					if stage == "create" && r.URL.Path == "/files" {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, `{"id":"file_synthetic_retained"}`)
						return
					}
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					connection.Close()
				}))
				defer server.Close()
				got := runReadableCommand(t, server, "--format-error", format, "batches", "submit", "--file", path, "--endpoint", "/v1/responses", "--completion-window", "24h")
				if got.code != 1 || got.stdout != "" || strings.Contains(got.stderr, server.URL) || strings.Contains(got.stderr, path) {
					t.Fatalf("transport failure status or privacy changed: %+v", got)
				}
				if format == "text" {
					if !strings.Contains(got.stderr, "Check your connection, proxy, and --base-url setting") {
						t.Fatalf("missing transport recovery: %s", got.stderr)
					}
				} else if !json.Valid([]byte(got.stderr)) || gjson.Get(got.stderr, "batch_submission.stage").String() != stage {
					t.Fatalf("machine recovery contract changed: %s", got.stderr)
				}
				if stage == "create" && !strings.Contains(got.stderr, "file_synthetic_retained") {
					t.Fatalf("lost uploaded file ID: %s", got.stderr)
				}
				mu.Lock()
				defer mu.Unlock()
				want := []string{"/files"}
				if stage == "create" {
					want = append(want, "/batches")
				}
				if !reflect.DeepEqual(requests, want) {
					t.Fatalf("mutations retried: %v", requests)
				}
			})
		}
	}
}

func TestMainBatchSubmitMissingReceiptRecovery(t *testing.T) {
	for _, stage := range []string{"upload", "create"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "synthetic.jsonl")
			if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.URL.Path)
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				if stage == "create" && r.URL.Path == "/files" {
					io.WriteString(w, `{"id":"file_synthetic_retained"}`)
					return
				}
				io.WriteString(w, `{"object":"synthetic_missing_receipt"}`)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "batches", "submit", "--file", path, "--endpoint", "/v1/responses", "--completion-window", "24h")
			if got.code != 1 || got.stdout != "" {
				t.Fatalf("missing receipt failure lost: %+v", got)
			}
			cause, recovery := "did not contain a file ID", "Check files list"
			want := []string{"/files"}
			if stage == "create" {
				cause, recovery = "did not contain a batch ID", "Check batches list"
				want = append(want, "/batches")
			}
			if !strings.Contains(got.stderr, cause) || !strings.Contains(got.stderr, recovery) {
				t.Fatalf("known cause or recovery omitted: %s", got.stderr)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("mutations retried: %v", requests)
			}
		})
	}
}
