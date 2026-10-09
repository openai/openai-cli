package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainDebugTimingRetriesKeepOutputModes(t *testing.T) {
	for _, flags := range [][]string{
		nil,
		{"--format", "json"},
		{"--format", "raw"},
		{"--format", "json", "--transform", "id", "--raw-output"},
		{"--quiet", "--format", "json"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/models/model_synthetic" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Private", "synthetic-private-header")
				if requests.Add(1)%2 == 1 {
					w.Header().Set("Retry-After-Ms", "1")
					w.WriteHeader(http.StatusServiceUnavailable)
					io.WriteString(w, `{"error":{"message":"synthetic-retry-body","type":"server_error"}}`)
					return
				}
				io.WriteString(w, `{"id":"synthetic-response-body","object":"model","created":0,"owned_by":"test"}`)
			}))
			t.Cleanup(server.Close)
			env := []string{"OPENAI_API_KEY=sk-fake-debug-timing", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
			args := append(append([]string{"openai"}, flags...), "models", "retrieve", "model_synthetic")
			control := runMainDispatchWithEnv(t, "bash", env, args...)
			debug := append([]string{"openai", "--debug"}, args[1:]...)
			got := runMainDispatchWithEnv(t, "bash", env, debug...)
			const fullDataHint = "Full data: --format json.\n"
			wantStderr, wantHints := "", 0
			if len(flags) == 0 {
				wantStderr, wantHints = fullDataHint, 1
			}
			if control.code != 0 || control.stderr != wantStderr || !strings.Contains(control.stdout, "synthetic-response-body") {
				t.Fatalf("control request failed: %+v", control)
			}
			if got.code != 0 || got.stdout != control.stdout || requests.Load() != 4 {
				t.Fatalf("debug changed output or retries: got=%+v control=%+v requests=%d", got, control, requests.Load())
			}
			if strings.Count(got.stderr, fullDataHint) != wantHints || !strings.HasSuffix(got.stderr, wantStderr) {
				t.Fatalf("debug changed the existing format hint: %q", got.stderr)
			}
			assertDebugTimingStage(t, got.stderr, 1, "response headers received", "503 Service Unavailable")
			assertDebugTimingStage(t, got.stderr, 2, "response headers received", "200 OK")
			assertDebugTimingStage(t, got.stderr, 2, "first response data read", "")
			assertDebugTimingStage(t, got.stderr, 2, "response body fully consumed", "")
			if strings.Count(got.stderr, "response headers received after") != 2 {
				t.Fatalf("retry attempt count changed: %q", got.stderr)
			}
			for _, private := range []string{"sk-fake-debug-timing", "synthetic-private-header", "synthetic-retry-body", "synthetic-response-body"} {
				if strings.Contains(got.stderr, private) {
					t.Errorf("debug disclosed %q: %q", private, got.stderr)
				}
			}
		})
	}
}

func TestMainDebugTimingStreamingBeforeCompletion(t *testing.T) {
	const firstEvent = `{"type":"response.output_text.delta","delta":"synthetic-first","sequence_number":0}`
	for _, tc := range []struct {
		name, prefix string
		flags        []string
	}{
		{"jsonl", firstEvent + "\n", []string{"--format", "jsonl"}},
		{"raw text", "synthetic-first\n", []string{"--format", "text", "--transform", "delta", "--raw-output"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, final := make(chan struct{}), make(chan struct{})
			var firstOnce, finalOnce sync.Once
			releaseFirst := func() { firstOnce.Do(func() { close(first) }) }
			releaseFinal := func() { finalOnce.Do(func() { close(final) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				select {
				case <-first:
				case <-r.Context().Done():
					return
				}
				writeStreamingTextEvent(w, firstEvent)
				w.(http.Flusher).Flush()
				select {
				case <-final:
				case <-r.Context().Done():
					return
				}
				writeStreamingTextEvent(w, `{"type":"response.output_text.delta","delta":"synthetic-last","sequence_number":1}`)
				writeStreamingTextEvent(w, `{"type":"response.completed","response":{"id":"resp_synthetic","status":"completed","output":[]}}`)
			}))
			t.Cleanup(server.Close)
			t.Cleanup(releaseFinal)
			t.Cleanup(releaseFirst)
			args := streamingTextArgs("responses", append([]string{"--debug"}, tc.flags...)...)
			child, stdout, stderr, ctx := startDebugTimingProcess(t, server, args...)
			stderr.waitFor(t, ctx, "response headers received after")
			if log := stderr.String(); strings.Contains(log, "first response data read") || strings.Contains(log, "response body fully consumed") {
				t.Fatalf("headers were labeled as response data or completion: %q", log)
			}
			releaseFirst()
			readStreamingTextPrefix(t, ctx, stdout, tc.prefix)
			stderr.waitFor(t, ctx, "first response data read after")
			if log := stderr.String(); strings.Contains(log, "response body fully consumed") {
				t.Fatalf("body completed before the withheld final event: %q", log)
			}
			releaseFinal()
			rest, err := io.ReadAll(stdout)
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil {
				t.Fatalf("stream failed: %v; stderr=%q", err, stderr.String())
			}
			log := stderr.String()
			headers := assertDebugTimingStage(t, log, 1, "response headers received", "200 OK")
			data := assertDebugTimingStage(t, log, 1, "first response data read", "")
			complete := assertDebugTimingStage(t, log, 1, "response body fully consumed", "")
			if headers > data || data > complete {
				t.Fatalf("timing milestones moved backward: headers=%d data=%d complete=%d", headers, data, complete)
			}
			for _, private := range []string{"synthetic input", "synthetic-first", "synthetic-last", "sk-fake-debug-timing"} {
				if strings.Contains(log, private) {
					t.Errorf("debug disclosed stream content %q: %q", private, log)
				}
			}
			control := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_API_KEY=sk-fake-debug-timing", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0",
			}, append([]string{"openai"}, streamingTextArgs("responses", tc.flags...)...)...)
			if control.code != 0 || control.stderr != "" || control.stdout != tc.prefix+string(rest) {
				t.Fatalf("debug changed streamed output: stdout=%q control=%+v", tc.prefix+string(rest), control)
			}
		})
	}
}

func TestMainDebugTimingEarlyStreamClose(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamingTextEvent(w, `{"type":"response.output_text.delta","delta":"synthetic-first","sequence_number":0}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	t.Cleanup(server.CloseClientConnections)
	args := streamingTextArgs("responses", "--debug", "--format", "jsonl")
	args = append(args, "--max-items", "1")
	got := runMainDispatchWithEnv(t, "bash", []string{
		"OPENAI_API_KEY=sk-fake-debug-timing", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0",
	}, append([]string{"openai"}, args...)...)
	if got.code != 0 || !strings.Contains(got.stdout, "synthetic-first") {
		t.Fatalf("early stream exit changed: %+v", got)
	}
	assertDebugTimingStage(t, got.stderr, 1, "response body closed before EOF", "")
	if strings.Contains(got.stderr, "response body fully consumed") {
		t.Fatalf("early close was labeled complete: %q", got.stderr)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("early stream exit retained the response body")
	}
}

func TestMainDebugTimingAPIFailurePreservesExitStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"synthetic failure","type":"invalid_request_error"}}`)
	}))
	t.Cleanup(server.Close)
	env := []string{"OPENAI_API_KEY=sk-fake-debug-timing", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	args := []string{"openai", "--quiet", "--format-error", "json", "models", "retrieve", "model_synthetic"}
	control := runMainDispatchWithEnv(t, "bash", env, args...)
	got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--debug"}, args[1:]...)...)
	if control.code == 0 || control.stdout != "" || !strings.Contains(control.stderr, "synthetic failure") {
		t.Fatalf("control API failure changed: %+v", control)
	}
	if got.code != control.code || got.stdout != "" || !strings.HasSuffix(got.stderr, control.stderr) {
		t.Fatalf("debug changed the API error or exit status: got=%+v control=%+v", got, control)
	}
	assertDebugTimingStage(t, got.stderr, 1, "response headers received", "400 Bad Request")
	assertDebugTimingStage(t, got.stderr, 1, "response body fully consumed", "")
}

func TestMainDebugTimingLargePayloads(t *testing.T) {
	// These sequential probes preserve accepted large bodies. Their size is
	// intentional, not a new API maximum. Keep each SSE line below 32 MiB.
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			size, repetitions := 70<<20, 1
			if stream {
				size, repetitions = 24<<20, 3
			}
			payload := strings.Repeat("x", size)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					for i := range repetitions {
						fmt.Fprintf(w, `data: {"type":"response.output_text.delta","item_id":"msg_large","output_index":0,"content_index":0,"sequence_number":%d,"delta":"`, i)
						io.WriteString(w, payload)
						io.WriteString(w, "\"}\n\n")
						w.(http.Flusher).Flush()
					}
					writeStreamingTextEvent(w, `{"type":"response.completed","response":{"status":"completed","output":[]}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"resp_large","object":"response","status":"completed","output":[{"id":"msg_large","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"`)
				io.WriteString(w, payload)
				io.WriteString(w, `","annotations":[]}]}]}`)
			}))
			t.Cleanup(server.Close)
			args := []string{"--debug", "--format", "json", "--transform", "output.0.content.0.text", "--raw-output",
				"responses", "create", "--model", "fake-model", "--input", "synthetic input"}
			if stream {
				args = streamingTextArgs("responses", "--debug", "--format", "text")
			}
			child, stdout, stderr, _ := startDebugTimingProcess(t, server, args...)
			gotHash := sha256.New()
			n, err := io.Copy(gotHash, stdout)
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil {
				t.Fatalf("large response failed: %v; stderr=%q", err, stderr.String())
			}
			wantHash := sha256.New()
			for range repetitions {
				io.WriteString(wantHash, payload)
			}
			io.WriteString(wantHash, "\n")
			if n != int64(size*repetitions+1) || !bytes.Equal(gotHash.Sum(nil), wantHash.Sum(nil)) {
				t.Fatalf("large response changed: got %d output bytes, want %d", n, size*repetitions+1)
			}
			log := stderr.String()
			assertDebugTimingStage(t, log, 1, "response headers received", "200 OK")
			assertDebugTimingStage(t, log, 1, "first response data read", "")
			assertDebugTimingStage(t, log, 1, "response body fully consumed", "")
			for _, private := range []string{"xxxxxxxxxxxxxxxx", "synthetic input", "sk-fake-debug-timing"} {
				if strings.Contains(log, private) {
					t.Errorf("debug disclosed large-payload content %q", private)
				}
			}
		})
	}
}

func assertDebugTimingStage(t *testing.T, log string, attempt int, stage, status string) int64 {
	t.Helper()
	pattern := regexp.QuoteMeta(fmt.Sprintf("HTTP attempt %d: %s after ", attempt, stage)) + `([0-9]+) ms`
	if status != "" {
		pattern += regexp.QuoteMeta(" (" + status + ")")
	}
	pattern += `\n`
	matches := regexp.MustCompile(pattern).FindAllStringSubmatch(log, -1)
	if len(matches) != 1 {
		t.Fatalf("want one %q timing for attempt %d: %q", stage, attempt, log)
	}
	elapsed, err := strconv.ParseInt(matches[0][1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return elapsed
}

type debugTimingCapture struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func (c *debugTimingCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	n, err := c.buffer.Write(p)
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (c *debugTimingCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.String()
}

func (c *debugTimingCapture) waitFor(t *testing.T, ctx context.Context, text string) {
	t.Helper()
	for !strings.Contains(c.String(), text) {
		select {
		case <-c.changed:
		case <-ctx.Done():
			t.Fatalf("diagnostic %q did not arrive before the next body stage: %q", text, c.String())
		}
	}
}

func startDebugTimingProcess(t *testing.T, server *httptest.Server, args ...string) (*exec.Cmd, io.ReadCloser, *debugTimingCapture, context.Context) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)...)
	home := t.TempDir()
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-debug-timing",
		"OPENAI_BASE_URL=" + server.URL, "HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home,
		"LOCALAPPDATA=" + home, "XDG_CONFIG_HOME=" + home, "GOMAXPROCS=2", "FORCE_COLOR=0"}
	stderr := &debugTimingCapture{changed: make(chan struct{}, 1)}
	child.Stderr = stderr
	child.WaitDelay = time.Second
	stdout, err := child.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		cancel()
		stdout.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		stdout.Close()
		if child.ProcessState == nil {
			child.Wait()
		}
	})
	return child, stdout, stderr, ctx
}
