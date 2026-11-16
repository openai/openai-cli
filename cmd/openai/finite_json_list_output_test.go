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
	"sync/atomic"
	"testing"
	"time"
)

func TestMainDispatchFiniteJSONListReadOnlyStdoutFails(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) != 1 || r.Method != http.MethodGet || r.URL.Path != "/files" || r.URL.Query().Get("after") != "" {
			t.Errorf("unexpected list request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
			return
		}
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
	setFiniteJSONListOutputProcess(child)
	var stderr bytes.Buffer
	child.Stdout, child.Stderr = destination, &stderr
	runErr := child.Run()
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
		t.Fatalf("read-only stdout did not return CLI failure: %v stderr=%q", runErr, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("read-only stdout made %d API requests, want 1", requests.Load())
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
