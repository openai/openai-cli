package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

const terminalRecoveryOutput = "{\"custom_id\":\"synthetic-success\",\"response\":{\"status_code\":200}}\n"
const terminalRecoveryErrors = "{\"custom_id\":\"synthetic-failure\",\"error\":{\"code\":\"invalid_request\"}}\n"

type submitRecoveryFixture struct {
	server                  *httptest.Server
	uploads, creates, polls atomic.Int32
	unexpected              atomic.Int32
	pollStarted             chan struct{}
}

func newSubmitRecoveryFixture(t *testing.T, status string, denyError, holdPoll bool) *submitRecoveryFixture {
	t.Helper()
	f := &submitRecoveryFixture{pollStarted: make(chan struct{})}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /files":
			f.uploads.Add(1)
			_, _ = io.WriteString(w, `{"id":"file_recovery_input","purpose":"batch"}`)
		case "POST /batches":
			f.creates.Add(1)
			_, _ = io.WriteString(w, `{"id":"batch_recovery","status":"validating","input_file_id":"file_recovery_input"}`)
		case "GET /batches/batch_recovery":
			if f.polls.Add(1) == 1 {
				close(f.pollStarted)
			}
			if holdPoll {
				<-r.Context().Done()
				return
			}
			batch := map[string]any{
				"id": "batch_recovery", "status": status, "input_file_id": "file_recovery_input",
				"request_counts": map[string]int{"total": 3, "completed": 0, "failed": 0},
			}
			if status == "failed" {
				batch["errors"] = map[string]any{"data": []any{map[string]any{"code": "invalid_request", "line": 2, "message": "Synthetic validation failure"}}}
			} else if status != "in_progress" {
				batch["request_counts"] = map[string]int{"total": 3, "completed": 2, "failed": 1}
				batch["output_file_id"], batch["error_file_id"] = "file_recovery_output", "file_recovery_error"
			}
			_ = json.NewEncoder(w).Encode(batch)
		case "GET /files/file_recovery_output/content":
			_, _ = io.WriteString(w, terminalRecoveryOutput)
		case "GET /files/file_recovery_error/content":
			if denyError {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":{"message":"Missing scopes: api.files.read.","type":"invalid_request_error","code":"synthetic_denied"}}`)
				return
			}
			_, _ = io.WriteString(w, terminalRecoveryErrors)
		default:
			f.unexpected.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func assertSubmitRecoveryIdentity(t *testing.T, f *submitRecoveryFixture, got mainDispatchResult, format string, code int) string {
	t.Helper()
	if got.code != code || f.uploads.Load() != 1 || f.creates.Load() != 1 || f.unexpected.Load() != 0 {
		t.Fatalf("recovery changed exit or remote mutations: result=%+v uploads=%d creates=%d unexpected=%d", got, f.uploads.Load(), f.creates.Load(), f.unexpected.Load())
	}
	message := got.stderr
	if format == "json" {
		if !json.Valid([]byte(got.stderr)) || gjson.Get(got.stderr, "batch_submission.batch_id").String() != "batch_recovery" {
			t.Fatalf("structured recovery lost its batch: %s", got.stderr)
		}
		message = gjson.Get(got.stderr, "batch_submission.message").String()
	}
	if !strings.Contains(message, "batch_recovery") || !strings.Contains(message, "Do not repeat submit") {
		t.Fatalf("recovery lost confirmed identity or duplicate prevention: %s", message)
	}
	return message
}

func TestMainBatchSubmitTerminalRecovery(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "expired", "completed"} {
		for _, format := range []string{"text", "json"} {
			t.Run(status+"/"+format, func(t *testing.T) {
				f := newSubmitRecoveryFixture(t, status, false, false)
				destination := filepath.Join(t.TempDir(), "results")
				got := runReadableCommand(t, f.server, "--format", "json", "--format-error", format, "batches", "submit", simpleBatchPath(t, simpleBatchInput), "--wait", "--output-dir", destination)
				message := assertSubmitRecoveryIdentity(t, f, got, format, 1)
				if strings.Contains(strings.ToLower(message), "resume") || strings.Contains(message, "batches wait") {
					t.Errorf("terminal batch incorrectly recommends more waiting: %s", message)
				}
				if !strings.Contains(strings.ToLower(message), "inspect") && !strings.Contains(message, "batches results") {
					t.Errorf("terminal batch lacks inspection guidance: %s", message)
				}
				if !json.Valid([]byte(got.stdout)) || gjson.Get(got.stdout, "status").String() != status {
					t.Fatalf("terminal response lost its status: %s", got.stdout)
				}
				if status == "failed" {
					if gjson.Get(got.stdout, "errors.data.0.code").String() != "invalid_request" {
						t.Fatal("terminal validation error was lost")
					}
					return
				}
				for name, want := range map[string]string{"output.jsonl": terminalRecoveryOutput, "error.jsonl": terminalRecoveryErrors} {
					data, err := os.ReadFile(filepath.Join(destination, name))
					if err != nil || string(data) != want {
						t.Fatalf("terminal result file %s was not preserved: %v", name, err)
					}
				}
			})
		}
	}
}

func TestMainBatchSubmitTerminalDownloadRecovery(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			f := newSubmitRecoveryFixture(t, "completed", true, false)
			destination := filepath.Join(t.TempDir(), "results")
			got := runReadableCommand(t, f.server, "--format", "json", "--format-error", format, "batches", "submit", simpleBatchPath(t, simpleBatchInput), "--wait", "--output-dir", destination)
			message := assertSubmitRecoveryIdentity(t, f, got, format, 1)
			if strings.Contains(message, "batches wait") || strings.Contains(strings.ToLower(message), "resume") {
				t.Errorf("download failure incorrectly recommends waiting: %s", message)
			}
			for _, want := range []string{"Could not save error.jsonl", "batches download", "Any completed files remain saved", "HTTP 403"} {
				if !strings.Contains(message, want) {
					t.Errorf("download recovery lost %q: %s", want, message)
				}
			}
			if data, err := os.ReadFile(filepath.Join(destination, "output.jsonl")); err != nil || string(data) != terminalRecoveryOutput {
				t.Fatal("download failure lost the saved output")
			}
			if _, err := os.Stat(filepath.Join(destination, "error.jsonl")); !os.IsNotExist(err) {
				t.Fatal("failed error download published a result file")
			}
			if gjson.Get(got.stdout, "status").String() != "completed" {
				t.Fatal("download failure lost the terminal batch response")
			}
		})
	}
}

func TestMainBatchSubmitInterruptedWaitRecovery(t *testing.T) {
	for _, reason := range []string{"timeout", "SIGINT"} {
		for _, format := range []string{"text", "json"} {
			t.Run(reason+"/"+format, func(t *testing.T) {
				if reason == "SIGINT" && runtime.GOOS == "windows" {
					t.Skip("os.Process.Signal cannot deliver SIGINT on Windows")
				}
				f := newSubmitRecoveryFixture(t, "in_progress", false, reason == "SIGINT")
				args := []string{"--format", "json", "--format-error", format, "batches", "submit", simpleBatchPath(t, simpleBatchInput), "--wait", "--poll-interval", "1h"}
				var got mainDispatchResult
				wantCode := 124
				if reason == "timeout" {
					got = runReadableCommand(t, f.server, append(args, "--wait-timeout", "250ms")...)
				} else {
					wantCode = 130
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					child := batchesWorkflowProcess(t, ctx, f.server, args...)
					var stdout, stderr bytes.Buffer
					child.Stdout, child.Stderr = &stdout, &stderr
					if err := child.Start(); err != nil {
						t.Fatal(err)
					}
					select {
					case <-f.pollStarted:
					case <-ctx.Done():
						t.Fatal("batch polling never started")
					}
					if err := child.Process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
					err := child.Wait()
					exit, ok := err.(*exec.ExitError)
					if !ok || ctx.Err() != nil {
						t.Fatalf("signal did not stop local waiting: %v", err)
					}
					got = mainDispatchResult{code: exit.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
				}
				message := assertSubmitRecoveryIdentity(t, f, got, format, wantCode)
				if !strings.Contains(strings.ToLower(message), "resume") || !strings.Contains(message, "batches wait") {
					t.Errorf("interrupted wait lost resume guidance: %s", message)
				}
				if got.stdout != "" || f.polls.Load() != 1 {
					t.Fatalf("interrupted wait printed a final response or repeated a poll: %+v", got)
				}
			})
		}
	}
}
