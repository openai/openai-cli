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
	"slices"
	"strings"
	"testing"
	"time"
)

// The server cannot send its terminal event until the consumer sees output.
// This tests the real stdout path, including its process-level pipe handling.
func TestMainDispatchOutputStructuredStreamEmitsBeforeNextEvent(t *testing.T) {
	for _, format := range []string{"json", "jsonl", "yaml", "raw", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			release := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamingTextEvent(w, `{"type":"response.output_text.delta","delta":"synthetic first event","sequence_number":0}`)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				writeStreamingTextEvent(w, `{"type":"response.completed","response":{"id":"resp_synthetic","status":"completed","output":[]}}`)
				io.WriteString(w, "data: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(release) })
			child, stdout, stderr, ctx := startStreamingTextCommand(t, server, streamingTextArgs("responses", "--format", format)...)
			prefix := make([]byte, 1)
			read := make(chan error, 1)
			go func() { _, err := io.ReadFull(stdout, prefix); read <- err }()
			select {
			case err := <-read:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("first event stayed buffered while the server withheld the next event")
			}
			release <- struct{}{}
			rest, err := io.ReadAll(stdout)
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil {
				t.Fatalf("stream failed: %v; stderr=%q", err, stderr.String())
			}
			if format != "explore" && stderr.Len() != 0 {
				t.Fatalf("unexpected diagnostics: %q", stderr.String())
			}
			output := string(prefix) + string(rest)
			if !strings.Contains(output, "synthetic first event") || !strings.Contains(output, "resp_synthetic") {
				t.Fatalf("missing event data: %q", output)
			}
			if format == "json" || format == "jsonl" || format == "raw" || format == "explore" {
				decoder := json.NewDecoder(strings.NewReader(output))
				for range 2 {
					var event map[string]any
					if err := decoder.Decode(&event); err != nil {
						t.Fatalf("invalid JSON record: %v", err)
					}
				}
				var extra any
				if err := decoder.Decode(&extra); err != io.EOF {
					t.Fatalf("unexpected trailing data: %v", err)
				}
			}
			if format == "jsonl" && strings.Count(output, "\n") != 2 {
				t.Fatalf("JSONL must emit exactly one line per event: %q", output)
			}
		})
	}
}

// Select this public lifecycle test in the existing native dispatch CI job.
func TestMainDispatchOutputEarlyStreamExitClosesSource(t *testing.T) {
	const event = `{"type":"response.output_text.delta","delta":"synthetic first event","sequence_number":0}`
	for _, format := range []string{"text", "jsonl"} {
		for _, limit := range []int{0, 1} {
			for _, quiet := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/limit=%d/quiet=%t", format, limit, quiet), func(t *testing.T) {
					closed := make(chan struct{})
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer close(closed)
						w.Header().Set("Content-Type", "text/event-stream")
						writeStreamingTextEvent(w, event)
						w.(http.Flusher).Flush()
						// No final event or EOF can stand in for early client closure.
						<-r.Context().Done()
					}))
					t.Cleanup(server.Close)
					t.Cleanup(server.CloseClientConnections)
					flags := []string{"--format", format}
					if quiet {
						flags = append(flags, "--quiet", "--verbose")
					}
					args := append(streamingTextArgs("responses", flags...), "--max-items", fmt.Sprint(limit))
					got := runOutputStreamBeforeProcessExit(t, server, closed, args...)
					want := ""
					if limit == 1 {
						want = event + "\n"
						if format == "text" {
							want = "synthetic first event\n"
						}
					}
					if got.code != 0 || got.stdout != want || got.stderr != "" {
						t.Fatalf("early exit changed bytes or status: got=%+v want stdout=%q", got, want)
					}
					select {
					case <-closed:
					case <-time.After(3 * time.Second):
						t.Fatal("early output exit retained the blocked HTTP stream")
					}
				})
			}
		}
	}
}

func runOutputStreamBeforeProcessExit(t *testing.T, server *httptest.Server, closed <-chan struct{}, args ...string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	argv := append([]string{"-test.run=^TestMainDispatchOutputStreamExitChild$", "--", "openai"}, args...)
	child := exec.CommandContext(ctx, binary, argv...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, "OPENAI_CLI_OUTPUT_STREAM_BARRIER="+home, "OPENAI_API_KEY=sk-fake-stream-close",
		"OPENAI_BASE_URL="+server.URL, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home,
		"LOCALAPPDATA="+home, "XDG_CONFIG_HOME="+home, "GOMAXPROCS=2", "FORCE_COLOR=0")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(home, "main-returned")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("stream child exited before main returned: %v stderr=%q", err, &stderr)
		case <-deadline.C:
			t.Fatal("stream main did not return before its bounded barrier")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("main returned without closing the HTTP source")
	}
	select {
	case err := <-done:
		t.Fatalf("process exit could have closed the source: %v", err)
	default:
	}
	if err := os.WriteFile(filepath.Join(home, "release-child"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	err = <-done
	if err != nil {
		t.Fatalf("stream child failed after release: %v stderr=%q", err, &stderr)
	}
	return mainDispatchResult{0, stdout.String(), stderr.String()}
}

func TestMainDispatchOutputStreamExitChild(t *testing.T) {
	directory := os.Getenv("OPENAI_CLI_OUTPUT_STREAM_BARRIER")
	if directory == "" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing stream child arguments")
	}
	os.Args = os.Args[separator+1:]
	main()
	if err := os.WriteFile(filepath.Join(directory, "main-returned"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(directory, "release-child")); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("parent did not release the stream child")
}
