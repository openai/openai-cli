//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

// Run under a private sized PTY. The capture checks one native frame for API
// and stdin cases, and three for progress, between COMPAT-BEGIN/END markers,
// for both Kitty and opted-in VS Code using the same resident output lifecycle.
func TestMainImageOutputSurvivesExecutableReplacementTerminal(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires actual terminal stdout")
	}
	sourcePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload := imageGenerationPNG(t)
	for _, program := range []string{"kitty", "vscode"} {
		for _, phase := range []string{"api", "stdin", "progress"} {
			for _, replace := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/replace-%v", program, phase, replace), func(t *testing.T) {
					dir := t.TempDir()
					live := filepath.Join(dir, "running cli")
					source, err := os.Open(sourcePath)
					if err != nil {
						t.Fatal(err)
					}
					copy, err := os.OpenFile(live, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
					if err != nil {
						source.Close()
						t.Fatal(err)
					}
					_, copyErr := io.Copy(copy, source)
					sourceErr := source.Close()
					closeErr := copy.Close()
					if copyErr != nil || sourceErr != nil || closeErr != nil {
						t.Fatalf("copy executable: %v, %v, %v", copyErr, sourceErr, closeErr)
					}
					change := func() error {
						if err := os.Rename(live, filepath.Join(dir, "original executable")); err != nil {
							return err
						}
						if replace {
							return os.WriteFile(live, []byte("#!/bin/sh\nexit 23\n"), 0700)
						}
						return nil
					}
					requestErr := make(chan error, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, err := io.Copy(io.Discard, r.Body)
						if err == nil && phase != "stdin" {
							err = change()
						}
						requestErr <- err
						if err != nil {
							http.Error(w, "synthetic failure", 500)
							return
						}
						if phase == "progress" {
							w.Header().Set("Content-Type", "text/event-stream")
							encoded := base64.StdEncoding.EncodeToString(payload)
							for index := 0; index < 2; index++ {
								fmt.Fprintf(w, "data: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":%d,\"b64_json\":%q}\n\n", index, encoded)
								w.(http.Flusher).Flush()
							}
							fmt.Fprintf(w, "data: {\"type\":\"image_generation.completed\",\"b64_json\":%q}\n\n", encoded)
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, imageGenerationResponse(payload))
						}
					}))
					defer server.Close()
					ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
					defer cancel()
					args := []string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "images", "generate", "--inline", "on", "--name", "result", "--output-dir", dir}
					if phase != "stdin" {
						args = append(args, "--prompt", "synthetic")
					}
					if phase == "progress" {
						args = append(args, "--partial-images", "2")
					}
					command := exec.CommandContext(ctx, live, args...)
					command.Env = append(imageGenerationEnv(server, dir), "PATH=/usr/bin:/bin", "TERM=xterm-256color", "TERM_PROGRAM="+program, "CI=false", "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
					if program == "vscode" {
						command.Env = append(command.Env, "OPENAI_VSCODE_IMAGES=1")
					}
					command.Stdout = os.Stdout
					var diagnostic bytes.Buffer
					command.Stderr = &diagnostic
					var input io.WriteCloser
					if phase == "stdin" {
						input, err = command.StdinPipe()
						if err != nil {
							t.Fatal(err)
						}
						defer input.Close()
					}
					fmt.Fprintln(os.Stdout, "COMPAT-BEGIN", t.Name())
					if err := command.Start(); err != nil {
						t.Fatal(err)
					}
					if phase == "stdin" {
						// Legal JSON whitespace exceeds ordinary Unix pipe capacity.
						// Completion proves the CLI is consuming it; withhold EOF and
						// the JSON body until its executable has changed.
						_, err = io.WriteString(input, strings.Repeat(" ", 4<<20))
						if err == nil {
							err = change()
						}
						if err == nil {
							_, err = io.WriteString(input, `{"prompt":"synthetic"}`)
						}
						closeErr := input.Close()
						if err != nil || closeErr != nil {
							command.Process.Kill()
							command.Wait()
							t.Fatalf("prepare stdin replacement: %v, %v", err, closeErr)
						}
					}
					if err := command.Wait(); err != nil {
						t.Fatalf("preview after replacement failed: %v; stderr=%q", err, diagnostic.String())
					}
					if diagnostic.Len() != 0 {
						t.Fatalf("unexpected diagnostic: %q", diagnostic.String())
					}
					select {
					case err := <-requestErr:
						if err != nil {
							t.Fatal(err)
						}
					default:
						t.Fatal("synthetic API request missing")
					}
					original, err := os.ReadFile(filepath.Join(dir, "result.png"))
					if err != nil || !bytes.Equal(original, payload) {
						t.Fatalf("saved original changed: %v", err)
					}
					fmt.Fprintln(os.Stdout, "COMPAT-END", t.Name())
				})
			}
		}
	}
}
