package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainShellMachineStreamDeliversBeforeNextEvent(t *testing.T) {
	for _, selection := range []string{"body", "flag"} {
		for _, format := range []string{"jsonl", "json", "pretty", "raw", "yaml"} {
			t.Run(selection+"/"+format, func(t *testing.T) {
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"first\",\"sequence_number\":0}\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"second\",\"sequence_number\":1}\n\n")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
				}))
				defer server.Close()
				released := false
				defer func() {
					if !released {
						close(release)
					}
				}()
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"openai", "--format", format}
				// JSON and JSONL retain the complete object. The other formats test
				// extraction and raw string bytes through the same stream path.
				extracted := format != "json" && format != "jsonl"
				if extracted {
					args = append(args, "--transform", "delta", "--raw-output")
				}
				args = append(args, "responses", "create", "--model", "fake-model", "--input", "synthetic")
				input := ""
				if selection == "body" {
					input = `{"stream":true}`
				} else {
					args = append(args, "--stream=true")
				}
				home := t.TempDir()
				env := []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
					"OPENAI_API_KEY=sk-fake-shell-stream", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--"}, args...)...)
				child.Env = env
				child.Stdin = strings.NewReader(input)
				child.WaitDelay = time.Second
				var stderr bytes.Buffer
				child.Stderr = &stderr
				stdout, err := child.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() {
					cancel()
					if child.ProcessState == nil {
						_ = child.Wait()
					}
				}()
				var captured bytes.Buffer
				reader := bufio.NewReader(io.TeeReader(stdout, &captured))
				type firstResult struct {
					value string
					err   error
				}
				first := make(chan firstResult, 1)
				go func() {
					if extracted {
						line, err := reader.ReadString('\n')
						first <- firstResult{strings.TrimSuffix(line, "\n"), err}
						return
					}
					var event struct{ Delta string }
					err := json.NewDecoder(reader).Decode(&event)
					first <- firstResult{event.Delta, err}
				}()
				select {
				case got := <-first:
					if got.err != nil || got.value != "first" {
						t.Fatalf("first event=%q error=%v", got.value, got.err)
					}
				case <-ctx.Done():
					t.Fatal("first event was buffered while the next event was withheld")
				}
				close(release)
				released = true
				if _, err := io.Copy(io.Discard, reader); err != nil {
					t.Fatal(err)
				}
				if err := child.Wait(); err != nil || ctx.Err() != nil || stderr.Len() != 0 {
					t.Fatalf("stream failed: %v context=%v stderr=%q", err, ctx.Err(), stderr.String())
				}
				control := runMainDispatchWithStdin(t, "", env, shellFileInput(t, []byte(input)), args...)
				if control.code != 0 || control.stderr != "" || control.stdout != captured.String() {
					t.Fatalf("delayed output changed: actual=%q control=%+v", captured.String(), control)
				}
			})
		}
	}
}
