package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Interrupt actual file writing, rather than request arrival. Streaming keeps
// the existing sub-32-MiB SSE fixture; JSON retains its larger payload probe.
func TestMainImageSavingInterruptRemovesIncompleteFile(t *testing.T) {
	for _, operation := range []string{"generate", "edit", "create-variation"} {
		for _, format := range []string{"text", "json"} {
			t.Run(operation+"/"+format, func(t *testing.T) {
				small := imageGenerationPNG(t)
				large := append(bytes.Clone(small), make([]byte, 49<<20)...)
				stream := operation == "edit"
				if stream {
					large = large[:(24<<20)-1024]
				}
				encoded := base64.StdEncoding.EncodeToString(large)
				response := fmt.Sprintf(`{"data":[{"b64_json":%q},{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(small), encoded)
				if stream {
					response = fmt.Sprintf("data: {\"type\":\"image_edit.completed\",\"b64_json\":%q}\n\n", encoded)
				}
				closed, stop := make(chan struct{}), make(chan struct{})
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					io.Copy(io.Discard, r.Body)
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
					} else {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Content-Length", strconv.Itoa(len(response)))
					}
					io.WriteString(w, response)
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
						close(closed)
					case <-stop:
					}
				}))
				defer server.Close()
				defer close(stop)
				dir, home := t.TempDir(), t.TempDir()
				args := []string{"openai", "images", operation, "--output-dir", dir, "--name", "interrupted", "--inline", "off", "--format-error", format}
				var source string
				if operation != "generate" {
					source = filepath.Join(t.TempDir(), "source.png")
					if err := os.WriteFile(source, small, 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--image", source)
				}
				if operation != "create-variation" {
					args = append(args, "--prompt", "Synthetic cancellation probe")
				}
				partial := filepath.Join(dir, "interrupted-2.png")
				if stream {
					args = append(args, "--stream", "true")
					partial = filepath.Join(dir, "interrupted.png")
				} else {
					args = append(args, "--count", "2")
				}
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				child, interrupt, verifyFixture := imageEditInterruptCommand(t, ctx, binary, home, imageGenerationEnv(server, home), args...)
				var stdout, stderr bytes.Buffer
				child.Stdout, child.Stderr = &stdout, &stderr
				started := time.Now()
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- child.Wait() }()
				waited := false
				defer func() {
					cancel()
					if !waited {
						<-done
					}
				}()
				var observed int64
				for observed == 0 {
					select {
					case err := <-done:
						waited = true
						t.Fatalf("image command exited before the active-write gate: %v", err)
					case <-ctx.Done():
						t.Fatal("image command never reached active file writing")
					default:
					}
					if info, err := os.Stat(partial); err == nil && info.Size() > 0 {
						observed = info.Size()
						if observed >= int64(len(large)) {
							t.Fatal("fixture missed active writing; a completed save cannot qualify interruption")
						}
						break
					}
					time.Sleep(100 * time.Microsecond)
				}
				if err := interrupt(); err != nil {
					t.Fatal(err)
				}
				var waitErr error
				select {
				case waitErr = <-done:
					waited = true
				case <-ctx.Done():
					t.Fatal("interrupted image command did not stop")
				}
				if code := child.ProcessState.ExitCode(); code != 130 {
					t.Errorf("interrupt status=%d, want 130; stderr=%q", code, stderr.String())
				}
				if err := verifyFixture(waitErr, stderr.Bytes(), time.Since(started)); err != nil {
					t.Errorf("interrupt fixture: %v", err)
				}
				if info, err := os.Stat(partial); !os.IsNotExist(err) {
					if err != nil {
						t.Errorf("inspect incomplete file: %v", err)
					} else {
						t.Errorf("incomplete image remains: gate=%d final=%d expected=%d", observed, info.Size(), len(large))
					}
				}
				message := stderr.String()
				if format == "json" {
					var document map[string]any
					if err := json.Unmarshal(stderr.Bytes(), &document); err != nil {
						t.Errorf("invalid cancellation JSON: %v; stderr=%q", err, stderr.String())
					}
					message, _ = document["message"].(string)
				}
				if stream {
					if strings.TrimSpace(message) != "Request canceled." || stdout.Len() != 0 {
						t.Errorf("zero-save cancellation: stdout=%q stderr=%q", stdout.String(), stderr.String())
					}
					if files := imageGenerationFiles(t, dir); len(files) != 0 {
						t.Errorf("zero-save cancellation left %d files", len(files))
					}
				} else {
					completed := filepath.Join(dir, "interrupted.png")
					data, err := os.ReadFile(completed)
					if err != nil || !bytes.Equal(data, small) {
						t.Errorf("completed image changed: %v", err)
					}
					if !strings.Contains(stdout.String(), strconv.Quote(completed)) || !strings.Contains(message, "Saved 1 image(s)") {
						t.Errorf("lost completed-file receipt or recovery: stdout=%q stderr=%q", stdout.String(), stderr.String())
					}
				}
				if source != "" {
					data, err := os.ReadFile(source)
					if err != nil || !bytes.Equal(data, small) {
						t.Errorf("source image changed: %v", err)
					}
				}
				if requests.Load() != 1 {
					t.Errorf("request count=%d, want 1", requests.Load())
				}
				select {
				case <-closed:
				case <-ctx.Done():
					t.Fatal("interrupted image command left its HTTP connection open")
				}
				t.Logf("active bytes=%d expected=%d exit=%d upload_present=%t", observed, len(large), child.ProcessState.ExitCode(), source != "")
			})
		}
	}
}
