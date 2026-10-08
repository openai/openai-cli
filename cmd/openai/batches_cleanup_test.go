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
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMainBatchesDownloadSIGTERMCleansOwnedStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support sending SIGTERM with os.Process.Signal")
	}
	const content = "{\"custom_id\":\"synthetic\"}\n"
	var slow atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("local termination mutated remote work: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/batches/batch_synthetic":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
		case "/files/file_output/content":
			w.Header().Set("Content-Type", "application/jsonl")
			if slow.Load() {
				w.Header().Set("Content-Length", fmt.Sprint(len(content)+4096))
				io.WriteString(w, content)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			io.WriteString(w, content)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.Error(w, "unexpected synthetic request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	directory := t.TempDir()
	previous := filepath.Join(directory, "published.jsonl")
	first := runReadableCommand(t, server, "batches", "download", "batch_synthetic", "--output", previous, "--format", "json")
	if first.code != 0 || first.stderr != "" || !json.Valid([]byte(first.stdout)) {
		t.Fatalf("initial download failed: %+v", first)
	}
	slow.Store(true)
	output := filepath.Join(directory, "next.jsonl")
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	child := batchesWorkflowProcess(t, ctx, server, "batches", "download", "batch_synthetic", "--output", output, "--format", "json")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	// Observe partial bytes in the staging file before sending the signal.
	// The server withholds the remaining body until cancellation closes HTTP.
	staged := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		paths, err := filepath.Glob(filepath.Join(directory, ".openai-batch-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 1 {
			info, err := os.Stat(paths[0])
			if err == nil && info.Size() == int64(len(content)) {
				staged = true
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !staged {
		t.Fatal("download never staged the first response bytes")
	}
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { completed <- child.Wait() }()
	select {
	case err := <-completed:
		waited = true
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("termination returned %v; stderr=%q", err, stderr.String())
		}
		status, signaled := exit.Sys().(syscall.WaitStatus)
		if exit.ExitCode() != 128+int(syscall.SIGTERM) && !(signaled && status.Signaled() && status.Signal() == syscall.SIGTERM) {
			t.Fatalf("termination lost signal status: %v; stderr=%q", err, stderr.String())
		}
		if exit.ExitCode() == 128+int(syscall.SIGTERM) && !json.Valid(stderr.Bytes()) {
			t.Fatalf("graceful termination corrupted JSON diagnostics: %q", stderr.String())
		}
	case <-time.After(2 * time.Second):
		_ = child.Process.Kill()
		<-completed
		waited = true
		t.Fatal("SIGTERM did not stop the download promptly")
	}
	if stdout.Len() != 0 {
		t.Fatalf("interrupted download emitted a success receipt: %q", stdout.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("interrupted output was published: %v", err)
	}
	contents, err := os.ReadFile(previous)
	if err != nil || string(contents) != content {
		t.Fatalf("termination damaged the previous download: bytes=%q error=%v", contents, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(previous) {
		t.Fatalf("termination left owned staging files: entries=%v error=%v", entries, err)
	}
	if requests.Load() != 4 {
		t.Fatalf("unexpected remote request count: %d; want two GETs per download", requests.Load())
	}
}

func TestMainBatchesDownloadRejectsDirectoryDestinationIntent(t *testing.T) {
	separators := []string{string(os.PathSeparator)}
	if runtime.GOOS == "windows" {
		separators = append(separators, "/")
	}
	for _, separator := range separators {
		for _, kind := range []string{"existing_directory", "double_separator", "missing_directory", "existing_file", "root"} {
			t.Run(fmt.Sprintf("%s_%q", kind, separator), func(t *testing.T) {
				directory := t.TempDir()
				const preserved = "existing synthetic bytes"
				target := filepath.Join(directory, "destination")
				path := target + separator
				switch kind {
				case "existing_directory", "double_separator":
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
					if kind == "double_separator" {
						path += separator
					}
				case "existing_file":
					if err := os.WriteFile(target, []byte(preserved), 0600); err != nil {
						t.Fatal(err)
					}
				case "root":
					path = filepath.VolumeName(directory) + separator
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodGet {
						t.Errorf("unexpected method: %s", r.Method)
					}
					if strings.HasPrefix(r.URL.Path, "/batches/") {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
						return
					}
					io.WriteString(w, batchesWorkflowContent)
				}))
				defer server.Close()
				got := runReadableCommand(t, server, "batches", "download", "batch_synthetic", "--output", path, "--format", "json")
				if got.code != 1 || got.stdout != "" || !json.Valid([]byte(got.stderr)) {
					t.Errorf("directory intent was lost: output=%q result=%+v", path, got)
				}
				entries, err := os.ReadDir(directory)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "existing_directory", "double_separator":
					children, err := os.ReadDir(target)
					if err != nil || len(entries) != 1 || len(children) != 0 {
						t.Errorf("directory destination gained nested output: entries=%v children=%v error=%v", entries, children, err)
					}
				case "existing_file":
					contents, err := os.ReadFile(target)
					if err != nil || string(contents) != preserved || len(entries) != 1 {
						t.Errorf("existing destination changed: bytes=%q entries=%v error=%v", contents, entries, err)
					}
				default:
					if len(entries) != 0 {
						t.Errorf("rejected output created files: %v", entries)
					}
				}
				if requests.Load() > 2 {
					t.Errorf("unexpected request count: %d", requests.Load())
				}
			})
		}
	}
}

func TestMainBatchesDownloadPreservesLiteralBackslashFilename(t *testing.T) {
	if os.IsPathSeparator('\\') {
		t.Skip("backslash is a directory separator on this platform")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasPrefix(r.URL.Path, "/batches/") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
			return
		}
		io.WriteString(w, batchesWorkflowContent)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "result\\")
	got := runReadableCommand(t, server, "batches", "download", "batch_synthetic", "--output", output, "--format", "json")
	content, err := os.ReadFile(output)
	if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) || err != nil || string(content) != batchesWorkflowContent || requests.Load() != 2 {
		t.Fatalf("valid literal filename changed: result=%+v contents=%q error=%v requests=%d", got, content, err, requests.Load())
	}
}
