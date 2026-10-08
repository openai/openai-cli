package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			for _, output := range []string{"", "-", "managed"} {
				t.Run(output, func(t *testing.T) {
					command := append([]string{}, args...)
					path := ""
					if output != "" {
						path = output
						if output == "managed" {
							path = filepath.Join(t.TempDir(), "copy with spaces.bin")
						}
						command = append(command, "--output", path)
					}
					got := runShellSaveCommand(t, server, command...)
					if got.code != 0 {
						t.Fatalf("command=%+v", got)
					}
					if output == "managed" {
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(data, payload) || got.stdout != "Wrote output to: "+path+"\n" || got.stderr != "" {
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

// Generated handlers retain their stdout receipts until the source-backed activation.
func TestMainShellSavePreservesLegacyReceiptForErrorFormats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "synthetic bytes") }))
	defer server.Close()
	for _, flags := range [][]string{
		nil, {"--format", "json"}, {"--format-error", "json"},
		{"--transform-error", "message"}, {"--format", "json", "--format-error", "text"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "copy.bin")
			args := append(append([]string{}, flags...), "files", "content", "file_synthetic", "--output", path)
			got := runShellSaveCommand(t, server, args...)
			if got.code != 0 || got.stdout != "Wrote output to: "+path+"\n" || got.stderr != "" {
				t.Fatalf("command=%+v", got)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic bytes" {
				t.Fatalf("saved=%q err=%v", data, err)
			}
		})
	}
}

func TestMainShellSpeechSaveFailurePreservesDestination(t *testing.T) {
	for _, tc := range []struct{ name, wire string }{
		{"empty stream", ""},
		{"heartbeat only", ": heartbeat\n\n"},
		{"DONE only", "data: [DONE]\n\n"},
		{"unknown only", "data: {\"type\":\"future.metadata\"}\n\n"},
		{"API stream failure", "event: error\ndata: {\"message\":\"synthetic failure\"}\n\n"},
		{"incomplete stream", "data: {\"type\":\"speech.audio.delta\",\"audio\":\"YQ==\"}\n\n"},
		{"malformed stream", "data: malformed\n\n"},
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
			got := runShellSaveCommand(t, server, "audio", "speech", "create", "--model", "tts-1", "--voice", "alloy", "--input", "synthetic", "--stream-format", "sse", "--output", path)
			if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "existing destination was not changed") {
				t.Fatalf("command=%+v", got)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "GOOD" {
				t.Fatalf("saved=%q err=%v", data, err)
			}
		})
	}
}
