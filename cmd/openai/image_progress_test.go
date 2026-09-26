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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
)

// Run this group under a sized PTY with OPENAI_CLI_PROGRESS_GATE_DIR set to a
// scratch directory. The observer creates <scenario> after seeing both partial
// labels; the fixture waits for that signal before sending the final response.
// It uses real CLI subprocesses and synthetic API responses, never native fonts.
func TestMainImageProgressTerminal(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires actual terminal stdout")
	}
	gateDirectory := os.Getenv("OPENAI_CLI_PROGRESS_GATE_DIR")
	require.NotEmpty(t, gateDirectory, "requires the PTY observer gate")
	for _, scenario := range []string{"generate", "edit", "stdin", "iterm", "off", "ci", "api", "malformed", "malformed-error-json", "failure", "final-first"} {
		t.Run(scenario, func(t *testing.T) {
			payload := imageGenerationPNG(t)
			encoded := base64.StdEncoding.EncodeToString(payload)
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "edit" {
					captured := readImageUpload(t, r)
					require.Equal(t, []string{"2"}, captured.values["partial_images"])
					require.Equal(t, []string{"true"}, captured.values["stream"])
				} else {
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, float64(2), body["partial_images"])
					require.Equal(t, true, body["stream"])
					if scenario == "api" {
						require.NotContains(t, body, "model")
					} else {
						require.Equal(t, defaultProgressModel, body["model"])
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				kind := "image_generation"
				if scenario == "edit" {
					kind = "image_edit"
				}
				if scenario != "final-first" {
					first := encoded
					if scenario == "malformed" || scenario == "malformed-error-json" {
						first = "private-invalid-progress"
					}
					fmt.Fprintf(w, "data: {\"type\":%q,\"partial_image_index\":0,\"b64_json\":%q}\n\n", kind+".partial_image", first)
					w.(http.Flusher).Flush()
					fmt.Fprintf(w, "data: {\"type\":%q,\"partial_image_index\":0,\"b64_json\":%q}\n\n", kind+".partial_image", first)
					fmt.Fprintf(w, "data: {\"type\":%q,\"partial_image_index\":1,\"b64_json\":%q}\n\n", kind+".partial_image", encoded)
					w.(http.Flusher).Flush()
				}
				if scenario == "generate" || scenario == "edit" || scenario == "stdin" || scenario == "iterm" || scenario == "failure" {
					deadline := time.NewTimer(5 * time.Second)
					defer deadline.Stop()
					poll := time.NewTicker(10 * time.Millisecond)
					defer poll.Stop()
				waitForObserver:
					for {
						if _, err := os.Stat(filepath.Join(gateDirectory, scenario)); err == nil {
							break
						}
						select {
						case <-poll.C:
						case <-r.Context().Done():
							return
						case <-deadline.C:
							t.Error("partial images did not reach the PTY before the final response")
							break waitForObserver
						}
					}
				}
				fmt.Fprintln(os.Stdout, "PROGRESS-FINAL-SEND", scenario)
				if scenario == "failure" || scenario == "malformed-error-json" {
					io.WriteString(w, "data: {\"type\":\"error\",\"message\":\"synthetic-private-api-error\"}\n\n")
					return
				}
				fmt.Fprintf(w, "data: {\"type\":%q,\"b64_json\":%q}\n\n", kind+".completed", encoded)
				w.(http.Flusher).Flush()
				if scenario != "api" {
					select {
					case <-r.Context().Done():
						close(closed)
					case <-time.After(5 * time.Second):
					}
				}
			}))
			defer server.Close()
			home := t.TempDir()
			temporary := filepath.Join(home, "temporary")
			require.NoError(t, os.Mkdir(temporary, 0700))
			env := append(imageGenerationEnv(server, home), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "TERM_PROGRAM=kitty", "TERM=xterm-256color", "CI=false", "TMPDIR="+temporary, "TMP="+temporary, "TEMP="+temporary)
			args := []string{"images", "generate", "--prompt", "synthetic progress", "--partial-images", "2", "--max-items", "-1"}
			var input io.Reader
			if scenario == "edit" {
				source, _ := imageUploadSource(t)
				args = append(imageUploadArgs("edit", source), "--partial-images", "2", "--max-items", "-1")
			}
			if scenario == "stdin" {
				args = []string{"images", "generate", "--prompt", "synthetic progress"}
				input = strings.NewReader(`{"partial_images":2}`)
			}
			if scenario == "off" {
				args = append(args, "--inline", "off")
			}
			if scenario == "ci" {
				env = append(env, "CI=true")
			}
			if scenario == "iterm" {
				env = append(env, "TERM_PROGRAM=iTerm.app")
			}
			if scenario == "api" {
				args = append([]string{"--format", "json"}, args...)
				args = append(args, "--stream", "true")
			}
			if scenario == "malformed-error-json" {
				args = append([]string{"--format-error", "json"}, args...)
			}
			binary, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)...)
			child.Env, child.Stdout, child.Stdin = env, os.Stdout, input
			var diagnostic bytes.Buffer
			child.Stderr = &diagnostic
			fmt.Fprintln(os.Stdout, "MAIN-PROGRESS-CASE", scenario)
			err = child.Run()
			fmt.Fprintln(os.Stdout, "MAIN-PROGRESS-END", scenario)
			require.NoError(t, ctx.Err())
			if scenario == "failure" || scenario == "malformed-error-json" {
				require.Error(t, err)
				require.Contains(t, diagnostic.String(), "before trying again")
				if scenario == "malformed-error-json" {
					require.True(t, json.Valid(diagnostic.Bytes()), diagnostic.String())
				}
			} else {
				require.NoError(t, err, diagnostic.String())
			}
			if scenario != "failure" && scenario != "malformed-error-json" {
				require.Empty(t, diagnostic.String())
			}
			require.NotContains(t, diagnostic.String(), "synthetic-private")
			files := imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"))
			if scenario == "failure" || scenario == "malformed-error-json" || scenario == "api" {
				require.Empty(t, files)
			} else {
				require.Len(t, files, 1)
				saved, err := os.ReadFile(files[0])
				require.NoError(t, err)
				require.Equal(t, payload, saved)
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("completed stream remained open")
				}
			}
			tempFiles, err := os.ReadDir(temporary)
			require.NoError(t, err)
			require.Empty(t, tempFiles, "progress must not leave temporary files")
		})
	}
}

// The API fixture asserts the current preset, independent of runtime internals.
const defaultProgressModel = "gpt-image-2.5-sunburst"
