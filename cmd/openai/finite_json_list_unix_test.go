//go:build !windows

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
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMainFiniteJSONListCancellationPreservesPartialOutput(t *testing.T) {
	requested := make(chan struct{})
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			io.WriteString(w, resourceFilePage([]string{"file_a"}, true))
			return
		}
		close(requested)
		<-r.Context().Done()
		close(disconnected)
	}))
	t.Cleanup(server.Close)
	child, stdout, stderr, ctx := startFiniteJSONListCommand(t, server)
	prefix := readFiniteJSONListFirst(t, ctx, stdout)
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal("next page was not requested")
	}
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil || ctx.Err() != nil {
		t.Fatalf("cancellation did not exit promptly with failure: %v stderr=%q", err, stderr.String())
	}
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("cancellation did not close the HTTP request")
	}
	assertFiniteJSONListPartial(t, prefix+string(rest), "file_a")
}

func TestMainFiniteJSONListClosedConsumerPreservesFailure(t *testing.T) {
	for _, upstreamFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("upstream_failure=%t", upstreamFailure), func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("after") == "" {
					io.WriteString(w, resourceFilePage([]string{"file_a"}, true))
					return
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if upstreamFailure {
					w.WriteHeader(http.StatusBadRequest)
					io.WriteString(w, `{"error":{"message":"synthetic later page failure","type":"invalid_request_error"}}`)
					return
				}
				io.WriteString(w, resourceFilePage([]string{"file_" + strings.Repeat("x", 16384)}, false))
			}))
			t.Cleanup(server.Close)
			child, stdout, stderr, ctx := startFiniteJSONListCommand(t, server)
			prefix := readFiniteJSONListFirst(t, ctx, stdout)
			if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			releaseOnce.Do(func() { close(release) })
			err := child.Wait()
			if ctx.Err() != nil || (err != nil) != upstreamFailure {
				t.Fatalf("closed consumer changed exit policy: %v stderr=%q", err, stderr.String())
			}
			assertFiniteJSONListPartial(t, prefix, "file_a")
			if !upstreamFailure {
				if stderr.Len() != 0 {
					t.Fatalf("lone EPIPE produced diagnostics: %q", stderr.String())
				}
				return
			}
			var diagnostic map[string]any
			if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
				t.Fatalf("invalid structured diagnostic: %q error=%v", stderr.String(), err)
			}
			if !strings.Contains(stderr.String(), "synthetic later page failure") {
				t.Fatalf("closed consumer suppressed upstream error: %q", stderr.String())
			}
		})
	}
}

func TestMainFiniteJSONListReadOnlyStdoutFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, resourceFilePage([]string{"file_a"}, false))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "read-only-output.json")
	const original = "existing synthetic content\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	// A read-only descriptor fails even for privileged users. This avoids chmod tests.
	destination, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "--format", "json", "files", "list")
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-list-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	var stderr bytes.Buffer
	child.Stdout, child.Stderr = destination, &stderr
	if err := child.Run(); err == nil || ctx.Err() != nil {
		t.Fatalf("read-only stdout did not fail promptly: %v stderr=%q", err, stderr.String())
	}
	var diagnostic map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil || len(diagnostic) == 0 {
		t.Fatalf("missing structured write error: %q error=%v", stderr.String(), err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != original {
		t.Fatal("failed output changed the existing destination")
	}
}
