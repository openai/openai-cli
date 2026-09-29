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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
)

// Run under scripts/check-image-loading.py. The observer releases each slow
// response only after feedback reaches the actual PTY; stream completion waits
// until a visible partial has remained free of loading redraws for 250 ms.
// These are synthetic process checks, not native image appearance tests.
func TestMainImageLoadingFeedbackTerminal(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires actual terminal stdout")
	}
	gateDirectory := os.Getenv("OPENAI_CLI_LOADING_GATE_DIR")
	require.NotEmpty(t, gateDirectory, "requires the PTY observer gate")
	for _, scenario := range []string{
		"generate", "edit", "variation", "stdin", "fast", "api-failure", "malformed", "save-failure", "cancel",
		"stream", "stream-interrupted", "dumb", "ci", "json", "error-json", "debug", "stdout-pipe", "stderr-pipe", "stdin-wait",
		"utf8", "no-color", "ascii", "locale-override",
	} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "stdin-wait" {
				fmt.Fprintln(os.Stdout, "MAIN-LOADING-CASE", scenario)
				checkImageLoadingInputInterrupt(t, true)
				fmt.Fprintln(os.Stdout, "MAIN-LOADING-END", scenario)
				return
			}
			if scenario == "cancel" && runtime.GOOS == "windows" {
				t.Skip("os.Process.Signal(os.Interrupt) is not available on Windows")
			}
			payload := imageGenerationPNG(t)
			home := t.TempDir()
			streaming := strings.HasPrefix(scenario, "stream")
			quiet := scenario == "json" || scenario == "error-json" || scenario == "debug" || strings.HasSuffix(scenario, "-pipe")
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			disconnected := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := io.Copy(io.Discard, r.Body)
				if err != nil {
					t.Errorf("read synthetic request: %v", err)
					return
				}
				if scenario == "cancel" {
					<-r.Context().Done()
					close(disconnected)
					return
				}
				if quiet {
					select {
					case <-time.After(650 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
				} else if scenario != "fast" && !waitImageLoadingGate(t, r.Context(), gateDirectory, scenario) {
					return
				}
				fmt.Fprintln(os.Stdout, "LOADING-RESPONSE-SEND", scenario)
				if scenario == "api-failure" || scenario == "error-json" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					io.WriteString(w, `{"error":{"message":"synthetic request rejected","type":"invalid_request_error","code":"invalid_api_key"}}`)
					return
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					encoded := base64.StdEncoding.EncodeToString(payload)
					fmt.Fprintf(w, "data: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":%q}\n\n", encoded)
					w.(http.Flusher).Flush()
					if !waitImageLoadingGate(t, r.Context(), gateDirectory, scenario+"-partial") {
						return
					}
					if scenario == "stream-interrupted" {
						return // EOF without a final event must remain a failure.
					}
					fmt.Fprintf(w, "data: {\"type\":\"image_generation.completed\",\"b64_json\":%q}\n\n", encoded)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if scenario == "save-failure" {
					// Remove only this test's empty output directory after preflight
					// succeeded. The API still returns valid image bytes.
					if err := os.Remove(filepath.Join(home, "Downloads", "gpt-images")); err != nil {
						t.Errorf("prepare saving failure: %v", err)
						return
					}
				}
				if scenario == "malformed" {
					io.WriteString(w, `{"data":[{"b64_json":"private-invalid-result"}]}`)
					return
				}
				io.WriteString(w, imageGenerationResponse(payload))
			}))
			defer server.Close()
			args := []string{"images", "generate", "--prompt", "synthetic feedback", "--inline", "off"}
			if scenario == "edit" || scenario == "variation" {
				operation := "edit"
				if scenario == "variation" {
					operation = "create-variation"
				}
				source, original := imageUploadSource(t)
				args = append(imageUploadArgs(operation, source), "--inline", "off")
				defer func() {
					data, err := os.ReadFile(source)
					require.NoError(t, err)
					require.Equal(t, original, data, "loading changed an uploaded source")
				}()
			}
			if streaming {
				args = []string{"images", "generate", "--prompt", "synthetic feedback", "--partial-images", "1", "--inline", "on"}
			}
			switch scenario {
			case "json":
				args = []string{"--format", "json", "images", "generate", "--prompt", "synthetic feedback"}
			case "error-json":
				args = append([]string{"--format-error", "json"}, args...)
			case "debug":
				args = append([]string{"--debug"}, args...)
			}
			child := imageLoadingCommand(t, ctx, server, home, args...)
			if scenario == "stdin" {
				child = imageLoadingCommand(t, ctx, server, home, "images", "generate", "--inline", "off")
				child.Stdin = strings.NewReader(`{"prompt":"synthetic feedback"}`)
			}
			if scenario == "dumb" {
				child.Env = append(child.Env, "TERM=dumb")
			}
			if scenario == "ci" {
				child.Env = append(child.Env, "CI=true")
			}
			switch scenario {
			case "utf8", "no-color":
				child.Env = append(child.Env, "LC_ALL=en_US.UTF-8", "NO_COLOR=", "CLICOLOR=", "FORCE_COLOR=")
				if scenario == "no-color" {
					child.Env = append(child.Env, "NO_COLOR=0")
				}
			case "ascii", "locale-override":
				child.Env = append(child.Env, "LC_ALL=C", "NO_COLOR=1")
				if scenario == "locale-override" {
					child.Env = append(child.Env, "LANG=en_US.UTF-8", "LC_CTYPE=UTF-8")
				}
			}
			child.Stdout, child.Stderr = os.Stdout, os.Stdout
			var redirected bytes.Buffer
			if scenario == "stdout-pipe" {
				child.Stdout = &redirected
			}
			if scenario == "stderr-pipe" {
				child.Stderr = &redirected
			}
			fmt.Fprintln(os.Stdout, "MAIN-LOADING-CASE", scenario)
			require.NoError(t, child.Start())
			if scenario == "cancel" {
				if !waitImageLoadingGate(t, ctx, gateDirectory, scenario) {
					cancel()
				} else {
					require.NoError(t, child.Process.Signal(os.Interrupt))
				}
			}
			err := child.Wait()
			fmt.Fprintln(os.Stdout, "MAIN-LOADING-END", scenario)
			require.NoError(t, ctx.Err())
			saved := imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"))
			switch scenario {
			case "cancel":
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 130, exit.ExitCode())
				select {
				case <-disconnected:
				case <-time.After(time.Second):
					t.Fatal("Ctrl-C did not cancel the HTTP request")
				}
				require.Empty(t, saved)
			case "api-failure", "error-json", "malformed", "save-failure", "stream-interrupted":
				require.Error(t, err)
				require.Empty(t, saved)
			case "json":
				require.NoError(t, err)
				require.Empty(t, saved)
			default:
				require.NoError(t, err)
				require.Len(t, saved, 1)
				data, err := os.ReadFile(saved[0])
				require.NoError(t, err)
				require.Equal(t, payload, data)
			}
			if scenario == "stderr-pipe" {
				require.Empty(t, redirected.String())
			}
			if scenario == "stdout-pipe" {
				require.Contains(t, redirected.String(), "Saved image:")
				require.NotContains(t, redirected.String(), "Generating image")
			}
		})
	}
}

func waitImageLoadingGate(t *testing.T, ctx context.Context, directory, name string) bool {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			t.Errorf("PTY observer did not release %s", name)
			return false
		case <-poll.C:
		}
	}
}

func imageLoadingCommand(t *testing.T, ctx context.Context, server *httptest.Server, home string, args ...string) *exec.Cmd {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)...)
	child.Env = append(imageGenerationEnv(server, home), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "TERM=xterm-256color", "TERM_PROGRAM=kitty", "CI=false", "APPDATA="+home, "XDG_CONFIG_HOME="+home, "TMPDIR="+home, "TMP="+home, "TEMP="+home)
	return child
}

func TestMainImageLoadingFeedbackPipesStayClean(t *testing.T) {
	payload := imageGenerationPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		time.Sleep(650 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, imageGenerationResponse(payload))
	}))
	defer server.Close()
	for _, format := range []string{"text", "json", "jsonl", "yaml"} {
		t.Run(format, func(t *testing.T) {
			home := t.TempDir()
			args := []string{"--format", format, "images", "generate", "--prompt", "synthetic feedback"}
			result := runImageGeneration(t, server, home, "", args...)
			require.Zero(t, result.code, result.stderr)
			require.Empty(t, result.stderr)
			require.NotContains(t, result.stdout, "Generating image")
			require.NotContains(t, result.stdout, "\x1b")
			if format == "text" {
				assertImageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"), result.stdout, 1, payload)
			} else {
				require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
				require.Contains(t, result.stdout, base64.StdEncoding.EncodeToString(payload))
				if format == "json" || format == "jsonl" {
					require.True(t, json.Valid([]byte(result.stdout)))
				}
			}
		})
	}
}

func TestMainImageLoadingFeedbackClosedDiagnosticsKeepsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix closed-pipe semantics")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"synthetic request rejected","type":"invalid_request_error"}}`)
	}))
	defer server.Close()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	home := t.TempDir()
	child := imageLoadingCommand(t, ctx, server, home, "images", "generate", "--prompt", "synthetic feedback")
	var stdout bytes.Buffer
	child.Stdout, child.Stderr = &stdout, writer
	require.Error(t, child.Run(), "a failed diagnostic write must not turn the API failure into success")
	require.NoError(t, ctx.Err())
	require.Empty(t, stdout.String())
	require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
}

func TestMainImageLoadingFeedbackDoesNotInterceptInputInterrupt(t *testing.T) {
	checkImageLoadingInputInterrupt(t, false)
}

func checkImageLoadingInputInterrupt(t *testing.T, terminal bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal(os.Interrupt) is not available on Windows")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	_, err = io.WriteString(writer, `{"prompt":"unfinished`)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	child := imageLoadingCommand(t, ctx, server, t.TempDir(), "images", "generate")
	var stdout, stderr bytes.Buffer
	child.Stdin, child.Stdout, child.Stderr = reader, &stdout, &stderr
	if terminal {
		child.Stdout, child.Stderr = os.Stdout, os.Stdout
	}
	require.NoError(t, child.Start())
	// Leave the JSON pipe open past the feedback delay. No request can begin
	// while the body is incomplete, and no loading worker may intercept Ctrl-C.
	time.Sleep(650 * time.Millisecond)
	require.NoError(t, child.Process.Signal(os.Interrupt))
	err = child.Wait()
	require.NoError(t, ctx.Err())
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, -1, exit.ExitCode(), "input preparation must retain default signal termination")
	require.Zero(t, requests.Load())
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())
}
