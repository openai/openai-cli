//go:build unix

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
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestMainBatchSubmitRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.jsonl")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("FIFO caused network mutation")
		w.WriteHeader(500)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "batches", "submit", "--file", path, "--endpoint", "/v1/responses", "--completion-window", "24h")
	if got.code == 0 || !strings.Contains(got.stderr, "regular local JSONL file") {
		t.Fatalf("FIFO was not rejected safely: %+v", got)
	}
}

func TestMainBatchSubmitInterruptedMutation(t *testing.T) {
	for _, phase := range []string{"upload", "create"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.jsonl")
			if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				io.Copy(io.Discard, r.Body)
				if phase == "create" && r.URL.Path == "/files" {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"file_saved_before_interrupt"}`)
					return
				}
				close(entered)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := batchesWorkflowProcess(t, ctx, server, "--format-error", "json", "batches", "submit", "--file", path, "--endpoint", "/v1/responses", "--completion-window", "24h")
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("mutation never started")
			}
			if err := child.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			err := child.Wait()
			exit, ok := err.(*exec.ExitError)
			wantRequests := int32(1)
			if phase == "create" {
				wantRequests = 2
			}
			if !ok || exit.ExitCode() != 130 || ctx.Err() != nil || stdout.Len() != 0 || !json.Valid(stderr.Bytes()) || requests.Load() != wantRequests {
				t.Fatalf("interrupted mutation lost outcome: err=%v requests=%d stdout=%q stderr=%q", err, requests.Load(), stdout.String(), stderr.String())
			}
			if gjson.GetBytes(stderr.Bytes(), "batch_submission.stage").String() != phase {
				t.Fatal("interrupted mutation lost recovery stage")
			}
			if phase == "create" && !strings.Contains(stderr.String(), "file_saved_before_interrupt") {
				t.Fatal("interruption lost uploaded file ID")
			}
		})
	}
}

func TestMainBatchSubmitFailedReceiptPreservesCreatedID(t *testing.T) {
	for _, failure := range []string{"interrupt", "closed_pipe"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.jsonl")
			if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/files" {
					io.WriteString(w, `{"id":"file_synthetic_uploaded"}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"id": "batch_confirmed", "status": "validating", "future_data": strings.Repeat("x", 8<<20)})
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			child := batchesWorkflowProcess(t, ctx, server, "--format", "json", "batches", "submit", "--file", path, "--endpoint", "/v1/responses", "--completion-window", "24h")
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdout.Close()
			var stderr bytes.Buffer
			child.Stderr = &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(stdout, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			wantExit := 1
			if failure == "interrupt" {
				wantExit = 130
				if err := child.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			} else if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			err = child.Wait()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != wantExit || ctx.Err() != nil || requests.Load() != 2 || !json.Valid(stderr.Bytes()) {
				t.Fatalf("failed receipt: err=%v requests=%d stderr=%q", err, requests.Load(), stderr.String())
			}
			if gjson.GetBytes(stderr.Bytes(), "batch_submission.batch_id").String() != "batch_confirmed" || !strings.Contains(stderr.String(), "Do not repeat submit") {
				t.Fatalf("created batch ID lost: %s", stderr.String())
			}
		})
	}
}
