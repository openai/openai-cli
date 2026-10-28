package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type batchStreamFirstRecord struct {
	buffer bytes.Buffer
	once   sync.Once
	seen   chan struct{}
}

func TestMainBatchResultsStopsAfterLateError(t *testing.T) {
	// Text can block while rendering the first record, before the parse failure exists.
	// Its late failure uses a consuming destination in TestMainBatchResultsTextLateError.
	for _, format := range []string{"json", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			filled := make(chan struct{})
			go func() {
				_, _ = writer.Write(bytes.Repeat([]byte("p"), 2*1024*1024))
				close(filled)
			}()
			defer func() { _ = reader.Close(); _ = writer.Close(); <-filled }()
			// Reading one byte proves the filler started. Keep the saturated pipe open.
			if _, err := reader.Read(make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/batches/batch_stream" {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"batch_stream","output_file_id":"file_stream"}`)
					return
				}
				io.WriteString(w, "{\"custom_id\":\"first-visible\",\"padding\":\""+strings.Repeat("x", 512)+"\"}\n{\"private\":")
			}))
			defer server.Close()
			child := batchesWorkflowProcess(t, ctx, server, "batches", "results", "batch_stream", "--format", format, "--format-error", "text")
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = writer, &stderr
			child.WaitDelay = time.Second
			err = child.Run()
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("late failure did not terminate with status 1: %v; context=%v; stderr=%s", err, ctx.Err(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "line 2") || !strings.Contains(stderr.String(), "Output may be incomplete") || strings.Contains(stderr.String(), "interrupted") {
				t.Fatalf("lost late failure or mislabeled cancellation: %s", stderr.String())
			}
		})
	}
}

func (w *batchStreamFirstRecord) Write(data []byte) (int, error) {
	n, err := w.buffer.Write(data)
	if bytes.Contains(w.buffer.Bytes(), []byte("first-visible")) {
		w.once.Do(func() { close(w.seen) })
	}
	return n, err
}

func TestMainBatchResultsTextLateError(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_stream" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"batch_stream","output_file_id":"file_stream"}`)
			return
		}
		io.WriteString(w, "{\"custom_id\":\"first-visible\",\"padding\":\""+strings.Repeat("x", 512)+"\"}\n{\"private\":")
	}))
	defer server.Close()
	child := batchesWorkflowProcess(t, ctx, server, "batches", "results", "batch_stream", "--format", "text")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	child.WaitDelay = time.Second
	err := child.Run()
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("late text failure lost its status: %v; context=%v; stderr=%s", err, ctx.Err(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "first-visible") || strings.Contains(stdout.String(), "private") {
		t.Fatalf("late text failure lost prior output or emitted malformed data: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "line 2") || !strings.Contains(stderr.String(), "Output may be incomplete") {
		t.Fatalf("late text failure lost recovery guidance: %s", stderr.String())
	}
}

func TestMainBatchResultsRecordStreaming(t *testing.T) {
	for _, format := range []string{"text", "json", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			output := &batchStreamFirstRecord{seen: make(chan struct{})}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/batches/batch_stream" {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"batch_stream","output_file_id":"file_stream"}`)
					return
				}
				io.WriteString(w, "{\"custom_id\":\"first-visible\",\"response\":{\"status_code\":200,\"body\":{\"output_text\":\"hello\"}}}\n")
				w.(http.Flusher).Flush()
				select {
				case <-output.seen:
					io.WriteString(w, "{\"custom_id\":\"second-visible\"}\n")
				case <-ctx.Done():
				}
			}))
			defer server.Close()
			child := batchesWorkflowProcess(t, ctx, server, "batches", "results", "batch_stream", "--format", format)
			missing := filepath.Join(t.TempDir(), "unavailable")
			child.Env = append(child.Env, "TMPDIR="+missing, "TMP="+missing, "TEMP="+missing)
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = output, &stderr
			child.WaitDelay = time.Second
			if err := child.Run(); err != nil || ctx.Err() != nil {
				t.Fatalf("first record did not unblock the producer: %v; context=%v; stderr=%s; requests=%d; stdout=%q", err, ctx.Err(), stderr.String(), requests.Load(), output.buffer.String())
			}
			if !bytes.Contains(output.buffer.Bytes(), []byte("second-visible")) {
				t.Fatal("the command lost the second record")
			}
			if format != "text" {
				var document struct {
					FileID string            `json:"file_id"`
					Data   []json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(output.buffer.Bytes(), &document); err != nil || document.FileID != "file_stream" || len(document.Data) != 2 {
					t.Fatalf("the command changed the result envelope: %v", err)
				}
			}
		})
	}
}
