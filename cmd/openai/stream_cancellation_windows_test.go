//go:build windows

package main

import (
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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The console delivers the real control event. Pipes preserve the public stream
// bytes; these cases do not claim terminal rendering or native shell parsing.
func testWindowsStreamCancellation(t *testing.T, binary string) {
	t.Helper()
	for _, name := range []string{"responses", "transcriptions", "speech"} {
		t.Run("stream cancellation/"+name, func(t *testing.T) {
			args := streamingTextArgs("responses")
			path := "/responses"
			event := `{"type":"response.output_text.delta","item_id":"msg_interrupt","output_index":0,"content_index":0,"delta":"Visible before interrupt"}`
			prefix := "Visible before interrupt"
			if name == "transcriptions" {
				args = readableAudioArgs(t, "audio:transcriptions", "--stream=true")
				path = "/audio/transcriptions"
				event = `{"type":"transcript.text.delta","delta":"Visible before interrupt"}`
			} else if name == "speech" {
				args = readableAudioArgs(t, "audio:speech", "--stream-format", "sse")
				path = "/audio/speech"
				event = `{"type":"speech.audio.delta","audio":"c3ludGhldGlj"}`
				prefix = "Type: speech.audio.delta"
			}

			var requests atomic.Int32
			disconnected, fixtureRelease := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) != 1 || r.Method != http.MethodPost || r.URL.Path != path {
					t.Errorf("unexpected stream request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
					return
				}
				// net/http observes disconnects after the POST body reaches EOF.
				_, readErr := io.Copy(io.Discard, r.Body)
				closeErr := r.Body.Close()
				if readErr != nil || closeErr != nil {
					t.Errorf("read synthetic request: %v; close: %v", readErr, closeErr)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamingTextEvent(w, event)
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					close(disconnected)
				case <-fixtureRelease:
					// Failure cleanup must not satisfy the disconnect assertion.
				}
			}))
			t.Cleanup(func() {
				close(fixtureRelease)
				server.CloseClientConnections()
				server.Close()
			})

			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			resultPath := filepath.Join(home, "console-result.json")
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := append([]string{"-test.run=^TestShellFileWindowsConsoleHelper$", "--", binary}, args...)
			process := exec.CommandContext(ctx, helper, command...)
			process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
			process.WaitDelay = 2 * time.Second
			for _, variable := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
				if value, ok := os.LookupEnv(variable); ok {
					process.Env = append(process.Env, variable+"="+value)
				}
			}
			process.Env = append(process.Env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home,
				"LOCALAPPDATA="+home, "TEMP="+home, "TMP="+home,
				"OPENAI_API_KEY=sk-fake-windows-stream", "OPENAI_BASE_URL="+server.URL,
				"OPENAI_CLI_TEST_CONSOLE_RESULT="+resultPath, "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb", "GOMAXPROCS=2")
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr adminConsoleCapture
			process.Stdout, process.Stderr = writer, &stderr
			input, err := process.StdinPipe()
			if err != nil {
				reader.Close()
				writer.Close()
				t.Fatal(err)
			}
			started, draining := false, false
			waitDone, drainDone := make(chan struct{}), make(chan struct{})
			var waitErr, drainErr error
			t.Cleanup(func() {
				input.Close() // EOF retains the helper's existing owned failure cleanup.
				cancel()
				writer.Close()
				if started {
					select {
					case <-waitDone:
					case <-time.After(4 * time.Second):
						process.Process.Kill()
						select {
						case <-waitDone:
						case <-time.After(3 * time.Second):
							t.Error("console helper did not join during cleanup")
						}
					}
				}
				reader.Close()
				if draining {
					select {
					case <-drainDone:
					case <-time.After(3 * time.Second):
						t.Error("stream capture did not join during cleanup")
					}
				}
			})
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			started = true
			go func() { waitErr = process.Wait(); close(waitDone) }()
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			firstCtx, stopFirst := context.WithTimeout(ctx, 5*time.Second)
			defer stopFirst()
			readStreamingTextPrefix(t, firstCtx, io.TeeReader(reader, &stdout), prefix)
			stopFirst()
			select {
			case <-waitDone:
				t.Fatalf("console child exited before control delivery: %v; stderr=%q", waitErr, stderr.text())
			case <-disconnected:
				t.Fatal("HTTP source closed before control delivery")
			default:
			}
			draining = true
			go func() { _, drainErr = io.Copy(&stdout, reader); close(drainDone) }()
			if _, err := input.Write([]byte{byte(windows.CTRL_C_EVENT)}); err != nil {
				t.Fatal(err)
			}
			if err := input.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Fatal(err)
			}
			select {
			case <-waitDone:
			case <-ctx.Done():
				t.Fatal("console helper did not exit after control delivery")
			}
			select {
			case <-drainDone:
			case <-ctx.Done():
				t.Fatal("stream capture remained open after console exit")
			}
			if drainErr != nil || ctx.Err() != nil {
				t.Fatalf("stream capture failed: read=%v; deadline=%v", drainErr, ctx.Err())
			}
			var exit *exec.ExitError
			if !errors.As(waitErr, &exit) || exit.ExitCode() == 0 {
				t.Fatalf("interrupted stream did not return a child failure: %v", waitErr)
			}
			data, err := os.ReadFile(resultPath)
			if err != nil {
				t.Fatalf("console helper result is missing: %v", err)
			}
			var result windowsConsoleResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("console helper result is invalid: %v", err)
			}
			if !result.Started || !result.EventSent || result.Event != windows.CTRL_C_EVENT || result.ChildExit == nil ||
				*result.ChildExit == 0 || *result.ChildExit != exit.ExitCode() || result.TimedOut || result.ForcedKill {
				t.Fatalf("helper failure cannot establish cancellation: result=%+v; helper exit=%d", result, exit.ExitCode())
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("interrupted CLI did not close its stalled HTTP stream")
			}
			out, diagnostic := stdout.checkedText(t), stderr.checkedText(t)
			if requests.Load() != 1 || !strings.HasPrefix(out, prefix) {
				t.Fatalf("partial output or request count changed: requests=%d stdout=%q", requests.Load(), out)
			}
			t.Logf("real CTRL_C event=%d child_exit=%d helper_exit=%d source_closed=true stdout=%q stderr=%q",
				result.Event, *result.ChildExit, exit.ExitCode(), out, diagnostic)
		})
	}
}
