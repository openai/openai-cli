package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every generated binary-save caller must inherit the handwritten receipt policy.
func TestMainDispatchOutputQuietBinarySaveReceipts(t *testing.T) {
	payload := []byte{0, 255, 254, 26, '\r', '\n', '\x1b', '[', '3', '1', 'm'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	for _, command := range shellBinarySaveCommands() {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			for _, nested := range []bool{false, true} {
				t.Run(map[bool]string{false: "root quiet", true: "nested quiet verbose"}[nested], func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "saved bytes.bin")
					args := append(append([]string{}, command...), "--output", path)
					if nested {
						args = append(args, "--quiet", "--verbose")
					} else {
						args = append([]string{"--quiet"}, args...)
					}
					got := runShellSaveCommand(t, server, args...)
					data, err := os.ReadFile(path)
					if got.code != 0 || got.stdout != "" || got.stderr != "" || err != nil || !bytes.Equal(data, payload) {
						t.Fatalf("quiet save changed data or receipt routing: result=%+v bytes=%x read=%v", got, data, err)
					}
				})
			}
		})
	}
}

func TestMainDispatchOutputSaveReceiptVerboseAndMachineModes(t *testing.T) {
	const payload = "synthetic\x00\xff\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, format string
		flags        []string
		receipt      bool
		verbose      bool
	}{
		{"verbose human", "auto", []string{"--verbose"}, true, true},
		{"quiet verbose", "auto", []string{"--quiet", "--verbose"}, false, false},
		{"inherited machine", "json", []string{"--verbose", "--format", "json"}, false, false},
		{"explicit machine", "auto", []string{"--verbose", "--format-error", "json"}, false, false},
		{"human override", "json", []string{"--verbose", "--format", "json", "--format-error", "text"}, true, true},
		{"quiet human override", "json", []string{"--quiet", "--verbose", "--format", "json", "--format-error", "text"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "copy.bin")
			args := append(append([]string{}, tc.flags...), "files", "content", "file_synthetic", "--output", path)
			got := runShellSaveCommand(t, server, args...)
			stderr := got.stderr
			if tc.verbose {
				stderr, _ = removeVerboseElapsed(t, stderr)
			}
			want := ""
			if tc.receipt {
				want = "Wrote output to: " + path + "\n"
			}
			if tc.verbose {
				want += "Command: files content\nFormat option: " + tc.format + "\nCommand result: completed\n"
			}
			data, err := os.ReadFile(path)
			if got.code != 0 || got.stdout != "" || stderr != want || err != nil || string(data) != payload {
				t.Fatalf("save composition changed: result=%+v want stderr=%q bytes=%x read=%v", got, want, data, err)
			}
		})
	}
}

func TestMainDispatchOutputQuietFailedSavePreservesDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = io.WriteString(w, "INCOMPLETE")
	}))
	defer server.Close()
	for _, machine := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "machine"}[machine], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "prior.bin")
			if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--quiet", "--verbose"}
			if machine {
				args = append(args, "--format-error", "json")
			}
			args = append(args, "files", "content", "file_synthetic", "--output", path)
			got := runShellSaveCommand(t, server, args...)
			if got.code != 1 || got.stdout != "" || got.stderr == "" || strings.Contains(got.stderr, "Wrote output to:") || strings.Contains(got.stderr, "Command result:") {
				t.Fatalf("quiet hid a failure or emitted a success receipt: %+v", got)
			}
			message := got.stderr
			if machine {
				var detail struct{ Message string }
				if err := json.Unmarshal([]byte(got.stderr), &detail); err != nil || detail.Message == "" {
					t.Fatalf("quiet save lost the complete JSON error message: %q (%v)", got.stderr, err)
				}
				message = detail.Message
			}
			if !strings.Contains(message, "Download incomplete") || !strings.Contains(message, "The existing destination was not changed.") {
				t.Fatalf("quiet save lost failure or destination guidance: %q", message)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "GOOD" {
				t.Fatalf("failed save changed prior data: %q %v", data, err)
			}
			stages, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".openai-download-*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("failed save retained staging: %v %v", stages, err)
			}
		})
	}
}
