package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainDispatchSpeechSaveMissingDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		payload           []byte
		flags             []string
	}{
		{
			name:        "binary",
			contentType: "audio/mpeg",
			payload:     []byte{0, 255, 254, 'b', 'i', 'n', '\n'},
		},
		{
			name:        "sse",
			contentType: "text/event-stream",
			payload: []byte("data: {\"type\":\"speech.audio.delta\",\"audio\":\"YQ==\"}\n\n" +
				"data: {\"type\":\"speech.audio.done\"}\n\ndata: [DONE]\n\n"),
			flags: []string{"--stream-format", "sse"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/audio/speech" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write(tc.payload)
			}))
			t.Cleanup(server.Close)
			directory := t.TempDir()
			parent := filepath.Join(directory, "missing parent")
			path := filepath.Join(parent, "speech output.bin")
			args := []string{"audio", "speech", "create", "--model", "tts-1", "--voice", "alloy", "--input", "synthetic", "--output", path}
			args = append(args, tc.flags...)

			got := runShellSaveCommand(t, server, args...)
			if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "Download could not start. The destination was not changed.") {
				t.Fatalf("missing-directory save=%+v", got)
			}
			if strings.Contains(got.stderr, "Wrote output to:") || strings.Contains(got.stderr, directory) {
				t.Fatalf("failed save reported success or exposed its private path: %q", got.stderr)
			}
			if requests.Load() != 1 {
				t.Fatalf("failed save made %d requests, want 1", requests.Load())
			}
			for _, absent := range []string{parent, path} {
				if _, err := os.Lstat(absent); !os.IsNotExist(err) {
					t.Fatalf("failed save created %q: %v", absent, err)
				}
			}
			assertShellCompletionStagesAbsent(t, directory)

			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			got = runShellSaveCommand(t, server, args...)
			if got.code != 0 || got.stdout != "" || got.stderr != "Wrote output to: "+path+"\n" {
				t.Fatalf("recovered save=%+v", got)
			}
			if requests.Load() != 2 {
				t.Fatalf("failure and recovery made %d requests, want 2", requests.Load())
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, tc.payload) {
				t.Fatalf("recovered bytes=%q error=%v, want %q", data, err, tc.payload)
			}
			assertShellCompletionStagesAbsent(t, directory)
			assertShellCompletionStagesAbsent(t, parent)
		})
	}
}
