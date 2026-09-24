//go:build darwin || linux

package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadCLITerminalOutput(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the standard-library pseudo-terminal harness")
	}
	binary := filepath.Join(t.TempDir(), "openai")
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	suffix := "\x1b[2J\x1b]2;synthetic\a\u009b31m\r\b\x7f\x9b\n\t世界☕"
	safeSuffix := `\u001b[2J\u001b]2;synthetic\u0007\u009b31m\r\b\u007f` + "�\n\t世界☕"
	for _, endpoint := range []struct {
		name, path string
		args       []string
	}{
		{"files", "/files/file_synthetic/content", []string{"files", "content", "file_synthetic"}},
		{"container files", "/containers/container_synthetic/files/file_synthetic/content", []string{"containers:files:content", "retrieve", "--container-id", "container_synthetic", "--file-id", "file_synthetic"}},
		{"skills", "/skills/skill_synthetic/content", []string{"skills:content", "retrieve", "skill_synthetic"}},
		{"skill versions", "/skills/skill_synthetic/versions/version_1/content", []string{"skills:versions:content", "retrieve", "--skill-id", "skill_synthetic", "--version", "version_1"}},
		{"audio", "/audio/speech", []string{"audio:speech", "create", "--input", "synthetic", "--model", "tts-1", "--voice", "alloy"}},
		{"videos", "/videos/video_synthetic/content", []string{"videos", "download-content", "video_synthetic"}},
		{"recordings", "/live/sessions/session_synthetic/content", []string{"live:sessions", "download-recording", "session_synthetic"}},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			for _, mode := range []string{"terminal", "pipe", "file", "raw stdout", "dev stdout", "large terminal"} {
				if mode == "large terminal" && endpoint.name != "files" {
					continue
				}
				t.Run(mode, func(t *testing.T) {
					prefix := strings.Repeat("A", 1024)
					if mode == "large terminal" {
						// Preserve large streamed downloads without adding a body cap.
						prefix = strings.Repeat("A", (32<<20)+1)
					}
					input := prefix + suffix
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != endpoint.path {
							t.Errorf("path = %q, want %q", r.URL.Path, endpoint.path)
						}
						w.Header().Set("Content-Type", "application/octet-stream")
						io.WriteString(w, prefix)
						w.(http.Flusher).Flush()
						for _, b := range []byte(suffix) {
							w.Write([]byte{b})
							w.(http.Flusher).Flush()
						}
					}))
					defer server.Close()
					args := append([]string{"--base-url", server.URL}, endpoint.args...)
					outfile := filepath.Join(t.TempDir(), "output.bin")
					switch mode {
					case "file":
						args = append(args, "--output", outfile)
					case "raw stdout":
						args = append(args, "--output", "-")
					case "dev stdout":
						args = append(args, "--output", "/dev/stdout")
					}
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, binary, args...)
					if mode != "pipe" {
						command = exec.CommandContext(ctx, python, append([]string{"-c", downloadPTYHarness, binary}, args...)...)
					}
					command.Env = []string{"OPENAI_API_KEY=sk-fake-download-test", "FORCE_COLOR=0"}
					var stderr bytes.Buffer
					command.Stderr = &stderr
					output, err := command.Output()
					if err != nil {
						t.Fatalf("CLI: %v\n%s", err, &stderr)
					}
					want := input
					if mode == "terminal" || mode == "large terminal" {
						want = prefix + safeSuffix
					}
					if mode == "file" {
						output, err = os.ReadFile(outfile)
						if err != nil {
							t.Fatal(err)
						}
					}
					if string(output) != want {
						t.Errorf("download bytes differ: got %d bytes, want %d", len(output), len(want))
					}
				})
			}
		})
	}
}

// Capture bytes from a real terminal without displaying them to the test runner.
// Raw terminal mode disables the OS's newline conversion for byte comparisons.
const downloadPTYHarness = `
import errno, os, pty, subprocess, sys, tty
master, slave = pty.openpty()
tty.setraw(slave)
process = subprocess.Popen(sys.argv[1:], stdin=subprocess.DEVNULL, stdout=slave)
os.close(slave)
try:
    while True:
        try:
            data = os.read(master, 65536)
        except OSError as error:
            if error.errno == errno.EIO:
                break
            raise
        if not data:
            break
        sys.stdout.buffer.write(data)
finally:
    os.close(master)
    process.wait()
sys.exit(process.returncode)
`
