package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runShellSaveCommand(t *testing.T, server *httptest.Server, args ...string) mainDispatchResult {
	t.Helper()
	home := t.TempDir()
	return runMainDispatchWithEnv(t, "", []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "OPENAI_API_KEY=sk-fake-shell-save", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}, append([]string{"openai"}, args...)...)
}

func TestMainShellBinarySaveCallers(t *testing.T) {
	payload := []byte{0, 255, 254, 'b', 'i', 'n', '\n'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"files", "content", "file_synthetic"},
		{"audio", "speech", "create", "--model", "tts-1", "--voice", "alloy", "--input", "synthetic"},
		{"containers", "files", "content", "retrieve", "cntr_synthetic", "file_synthetic"},
		{"live", "sessions", "download-recording", "sess_synthetic"},
		{"skills", "content", "retrieve", "skill_synthetic"},
		{"skills", "versions", "content", "retrieve", "skill_synthetic", "1"},
		{"videos", "download-content", "video_synthetic"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			for _, output := range []string{"", "-", "/dev/stdout", "managed", "alias", "structured", "transformed error"} {
				t.Run(output, func(t *testing.T) {
					command := append([]string{}, args...)
					path := ""
					managed := output != "" && output != "-" && output != "/dev/stdout"
					if output != "" {
						path = output
						if managed {
							path = filepath.Join(t.TempDir(), "copy with spaces.bin")
						}
						flag := "--output"
						if output == "alias" {
							flag = "-o"
						}
						command = append(command, flag, path)
					}
					receipt := "Wrote output to: " + path + "\n"
					if output == "structured" {
						command = append([]string{"--format-error", "json"}, command...)
						receipt = ""
					} else if output == "transformed error" {
						command = append([]string{"--transform-error", "message"}, command...)
						receipt = ""
					}
					got := runShellSaveCommand(t, server, command...)
					if got.code != 0 {
						t.Fatalf("command=%+v", got)
					}
					if managed {
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(data, payload) || got.stdout != "" || got.stderr != receipt {
							t.Fatalf("data=%x result=%+v", data, got)
						}
					} else if got.stdout != string(payload) || got.stderr != "" {
						t.Fatalf("binary stdout changed: %+v", got)
					}
				})
			}
		})
	}
}

func TestMainShellSavePreservesGoodDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "PARTIAL")
	}))
	defer server.Close()
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "destination.bin")
			if existing {
				if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got := runShellSaveCommand(t, server, "files", "content", "file_synthetic", "--output", path)
			if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "Download incomplete") {
				t.Fatalf("command=%+v", got)
			}
			data, err := os.ReadFile(path)
			if existing {
				if err != nil || string(data) != "GOOD" {
					t.Fatalf("prior destination changed: %q %v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("failed new destination exists: %q %v", data, err)
			}
			stages, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".openai-download-*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("stages=%v err=%v", stages, err)
			}
		})
	}
}

func TestMainShellSaveReceiptErrorFormats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "synthetic bytes") }))
	defer server.Close()
	for _, tc := range []struct {
		flags   []string
		receipt bool
	}{
		{nil, true}, {[]string{"--format", "json"}, false},
		{[]string{"--format", "yaml"}, false}, {[]string{"--format-error", "json"}, false},
		{[]string{"--transform-error", "message"}, false},
		{[]string{"--format", "json", "--format-error", "text"}, true},
		{[]string{"--format", "json", "--format-error", "auto"}, true},
		{[]string{"--format-error", "text", "--transform-error", "message"}, false},
	} {
		t.Run(strings.Join(tc.flags, " "), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "copy.bin")
			args := append(append([]string{}, tc.flags...), "files", "content", "file_synthetic", "--output", path)
			got := runShellSaveCommand(t, server, args...)
			stderr := ""
			if tc.receipt {
				stderr = "Wrote output to: " + path + "\n"
			}
			if got.code != 0 || got.stdout != "" || got.stderr != stderr {
				t.Fatalf("command=%+v", got)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic bytes" {
				t.Fatalf("saved=%q err=%v", data, err)
			}
		})
	}
}

func TestMainShellSaveReceiptEscapesPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "synthetic bytes") }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "copy\u202e.bin")
	got := runShellSaveCommand(t, server, "files", "content", "file_synthetic", "--output", path)
	want := "Wrote output to: " + strings.ReplaceAll(path, "\u202e", `\u202e`) + "\n"
	if got.code != 0 || got.stdout != "" || got.stderr != want {
		t.Fatalf("escaped receipt=%+v want=%q", got, want)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic bytes" {
		t.Fatalf("saved=%q err=%v", data, err)
	}
}

func TestMainShellSaveReceiptAfterRetryAndRedirect(t *testing.T) {
	for _, retry := range []bool{false, true} {
		for _, redirect := range []bool{false, true} {
			if !retry && !redirect {
				continue
			}
			t.Run(map[bool]string{false: "direct", true: "retry"}[retry]+"/"+map[bool]string{false: "final", true: "redirect"}[redirect], func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempt := requests.Add(1)
					if retry && attempt == 1 {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Retry-After", "0")
						w.WriteHeader(http.StatusTooManyRequests)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic retry","type":"rate_limit_error"}}`)
						return
					}
					if redirect && r.URL.Path != "/final" {
						http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
						return
					}
					_, _ = io.WriteString(w, "synthetic bytes")
				}))
				defer server.Close()
				path := filepath.Join(t.TempDir(), "copy.bin")
				got := runShellSaveCommand(t, server, "files", "content", "file_synthetic", "--output", path)
				wantRequests := int32(1)
				if retry {
					wantRequests++
				}
				if redirect {
					wantRequests++
				}
				if got.code != 0 || got.stdout != "" || got.stderr != "Wrote output to: "+path+"\n" || requests.Load() != wantRequests {
					t.Fatalf("receipt=%+v requests=%d want=%d", got, requests.Load(), wantRequests)
				}
				if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic bytes" {
					t.Fatalf("saved=%q err=%v", data, err)
				}
			})
		}
	}
}

func TestMainShellSaveReceiptClosedStderr(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "synthetic bytes") }))
	defer server.Close()
	for _, structured := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt write fails", true: "structured receipt suppressed"}[structured], func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			path := filepath.Join(home, "copy.bin")
			args := []string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}
			if structured {
				args = append(args, "--format-error", "json")
			}
			args = append(args, "files", "content", "file_synthetic", "--output", path)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, args...)
			child.WaitDelay = time.Second
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
					child.Env = append(child.Env, entry)
				}
			}
			child.Env = append(child.Env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "OPENAI_API_KEY=sk-fake-shell-save", "OPENAI_BASE_URL="+server.URL, "FORCE_COLOR=0")
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			child.Stdout, child.Stderr = &stdout, writer
			err = child.Run()
			if ctx.Err() != nil || (err == nil) != structured || stdout.Len() != 0 {
				t.Fatalf("closed stderr result=%v timeout=%v stdout=%q", err, ctx.Err(), &stdout)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic bytes" {
				t.Fatalf("saved=%q err=%v", data, err)
			}
		})
	}
}

func TestMainShellSpeechSaveCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		complete   bool
	}{
		{"empty stream", "", false},
		{"heartbeat only", ": heartbeat\n\n", false},
		{"DONE only", "data: [DONE]\n\n", false},
		{"unknown only", "data: {\"type\":\"future.metadata\"}\n\n", false},
		{"API stream failure", "event: error\ndata: {\"message\":\"synthetic failure\"}\n\n", false},
		{"incomplete stream", "data: {\"type\":\"speech.audio.delta\",\"audio\":\"YQ==\"}\n\n", false},
		{"malformed stream", "data: malformed\n\n", false},
		{"malformed after completion", "data: {\"type\":\"speech.audio.done\"}\n\ndata: malformed\n\n", false},
		{"error after completion", "data: {\"type\":\"speech.audio.done\"}\n\nevent: error\ndata: {\"message\":\"synthetic failure\"}\n\n", false},
		{"unterminated completion", "data: {\"type\":\"speech.audio.done\"}", true},
		{"completed stream", "data: {\"type\":\"speech.audio.delta\",\"audio\":\"YQ==\"}\n\ndata: {\"type\":\"speech.audio.done\"}\n\ndata: [DONE]\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.wire)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "speech.sse")
			if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
				t.Fatal(err)
			}
			got := runShellSaveCommand(t, server, "audio:speech", "create", "--model", "tts-1", "--voice", "alloy", "--input", "synthetic", "--stream-format", "sse", "--output", path)
			want := "GOOD"
			if tc.complete {
				want = tc.wire
				if got.code != 0 || got.stdout != "" || got.stderr != "Wrote output to: "+path+"\n" {
					t.Fatalf("completed speech=%+v", got)
				}
			} else if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "existing destination was not changed") {
				t.Fatalf("command=%+v", got)
			}
			if !tc.complete && strings.Contains(got.stderr, "Wrote output to:") {
				t.Fatalf("failed speech save emitted a success receipt: %q", got.stderr)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != want {
				t.Fatalf("saved=%q err=%v", data, err)
			}
			assertShellCompletionStagesAbsent(t, filepath.Dir(path))
		})
	}
}

func TestMainShellSpeechSaveTransportFailureAfterDone(t *testing.T) {
	const wire = "data: {\"type\":\"speech.audio.done\"}\n\ndata: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(wire)+1))
		_, _ = io.WriteString(w, wire)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "speech.sse")
	if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
		t.Fatal(err)
	}
	got := runShellSaveCommand(t, server, "audio", "speech", "create", "--model", "tts-1", "--voice", "alloy", "--input", "synthetic", "--stream-format", "sse", "--output", path)
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "existing destination was not changed") {
		t.Fatalf("truncated completed speech=%+v", got)
	}
	if strings.Contains(got.stderr, "Wrote output to:") {
		t.Fatalf("failed transfer emitted a success receipt: %q", got.stderr)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "GOOD" {
		t.Fatalf("prior file changed: %q %v", data, err)
	}
	stages, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".openai-download-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("stages=%v err=%v", stages, err)
	}
}
