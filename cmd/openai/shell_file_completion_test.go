package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMainShellEmptyBinarySave(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "0")
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"files", "content", "file_synthetic"},
		{"audio", "speech", "create", "--model", "tts-1", "--voice", "alloy", "--input", "synthetic"},
	} {
		for _, existing := range []bool{false, true} {
			t.Run(args[0]+"/existing="+strconv.FormatBool(existing), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "empty.bin")
				if existing {
					if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				command := append(append([]string{}, args...), "--output", path)
				got := runShellSaveCommand(t, server, command...)
				if got.code != 0 || got.stdout != "" || got.stderr != "Wrote output to: "+path+"\n" {
					t.Fatalf("empty binary save: %+v", got)
				}
				if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
					t.Fatalf("empty destination bytes=%q error=%v", data, err)
				}
				assertShellCompletionStagesAbsent(t, filepath.Dir(path))
			})
		}
	}
}

func TestMainShellSaveReceiptWaitsForBodyCompletion(t *testing.T) {
	const prefix, suffix = "synthetic prefix", " and completed tail"
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(prefix)+len(suffix)))
		_, _ = io.WriteString(w, prefix)
		w.(http.Flusher).Flush()
		close(started)
		<-release
		_, _ = io.WriteString(w, suffix)
	}))
	defer server.Close()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	path := filepath.Join(t.TempDir(), "output.bin")
	if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
		t.Fatal(err)
	}
	process, done, stdout, stderr := startShellCompletionCommand(t, server.URL, nil, nil, "files", "content", "file_synthetic", "--output", path)
	waitForShellCompletionRequest(t, started, done)
	// Observe actual staged bytes before inspecting receipts. The source stays
	// blocked until this state assertion completes; elapsed time is not ordering.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	staged := false
	for !staged {
		names, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".openai-download-*.tmp"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			data, err := os.ReadFile(name)
			staged = err == nil && string(data) == prefix
			if staged {
				break
			}
		}
		if staged {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("download exited before source completion: %v", err)
		case <-deadline.C:
			t.Fatal("staged prefix was not observed")
		case <-poll.C:
		}
	}
	if got := readShellCompletionFile(t, path); got != "GOOD" {
		t.Fatalf("destination changed before completion: %q", got)
	}
	if out, errout := readShellCompletionFile(t, stdout), readShellCompletionFile(t, stderr); out != "" || errout != "" {
		t.Fatalf("early output: stdout=%q stderr=%q", out, errout)
	}
	close(release)
	released = true
	if err := <-done; err != nil || process.ProcessState.ExitCode() != 0 {
		t.Fatalf("completed download: %v stderr=%q", err, readShellCompletionFile(t, stderr))
	}
	if got := readShellCompletionFile(t, path); got != prefix+suffix {
		t.Fatalf("completed destination=%q", got)
	}
	if out, errout := readShellCompletionFile(t, stdout), readShellCompletionFile(t, stderr); out != "" || errout != "Wrote output to: "+path+"\n" {
		t.Fatalf("completed output: stdout=%q stderr=%q", out, errout)
	}
	assertShellCompletionStagesAbsent(t, filepath.Dir(path))
}

func TestMainShellClosedStdoutStopsDownload(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = io.WriteString(w, strings.Repeat("synthetic bytes", 1024))
		w.(http.Flusher).Flush()
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release) // Keep the source blocked until the child exits.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	process, done, _, stderr := startShellCompletionCommand(t, server.URL, writer, nil, "files", "content", "file_synthetic", "--output", "-")
	waitForShellCompletionRequest(t, started, done)
	if err := <-done; process.ProcessState.ExitCode() != 1 {
		t.Fatalf("closed stdout must fail without source EOF: %v", err)
	}
	if got := readShellCompletionFile(t, stderr); !strings.Contains(got, "Download incomplete") || strings.Contains(got, "Wrote output to:") {
		t.Fatalf("closed stdout diagnostic=%q", got)
	}
}

func TestMainShellClosedStdoutPreservesStreamFailure(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["stream"] != true {
			t.Errorf("stream request=%v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"synthetic source failure\"}}}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release) // EOF must not substitute for the observed failure event.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader(`{"model":"synthetic","input":"test","stream":true}`)
	process, done, _, stderr := startShellCompletionCommand(t, server.URL, writer, input, "--format", "jsonl", "responses", "create")
	waitForShellCompletionRequest(t, started, done)
	if err := <-done; process.ProcessState.ExitCode() != 1 {
		t.Fatalf("source failure must survive closed stdout without EOF: %v", err)
	}
	var diagnostic struct{ Message string }
	got := readShellCompletionFile(t, stderr)
	if err := json.Unmarshal([]byte(got), &diagnostic); err != nil || !strings.Contains(diagnostic.Message, "streamed response failed") {
		t.Fatalf("original structured stream failure was lost: %q error=%v", got, err)
	}
}

func startShellCompletionCommand(t *testing.T, baseURL string, output *os.File, input io.Reader, args ...string) (*exec.Cmd, <-chan error, string, string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	stdout, stderr := filepath.Join(home, "stdout"), filepath.Join(home, "stderr")
	files := make([]*os.File, 0, 2)
	for _, path := range []string{stdout, stderr} {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		})
		files = append(files, file)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	command := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)
	process := exec.CommandContext(ctx, binary, command...)
	process.WaitDelay = time.Second
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
			process.Env = append(process.Env, entry)
		}
	}
	process.Env = append(process.Env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "OPENAI_API_KEY=sk-fake-shell-completion", "OPENAI_BASE_URL="+baseURL, "FORCE_COLOR=0")
	process.Stdout, process.Stderr = files[0], files[1]
	process.Stdin = input
	if output != nil {
		process.Stdout = output
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait(); close(done) }()
	t.Cleanup(func() {
		if ctx.Err() == context.DeadlineExceeded {
			t.Error("completion command exceeded its deadline")
		}
		cancel()
		<-done
	})
	return process, done, stdout, stderr
}

func waitForShellCompletionRequest(t *testing.T, started <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-started:
	case err := <-done:
		select {
		case <-started:
			// A fast failure can finish before the parent observes the request.
		default:
			t.Fatalf("command exited before loopback request: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loopback request did not arrive")
	}
}

func readShellCompletionFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertShellCompletionStagesAbsent(t *testing.T, directory string) {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(directory, ".openai-download-*.tmp"))
	if err != nil || len(names) != 0 {
		t.Fatalf("remaining staging files=%v error=%v", names, err)
	}
}
