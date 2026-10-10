//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMainSkillUploadFIFOFirstSignal(t *testing.T) {
	runSkillUploadCancellationSignals(t, func(t *testing.T, processSignal os.Signal, exitCode int) {
		for _, mode := range []string{"file open", "generic body reference"} {
			t.Run(mode, func(t *testing.T) {
				root, scratch, home := t.TempDir(), t.TempDir(), t.TempDir()
				fifo := filepath.Join(root, "pending.fifo")
				require.NoError(t, unix.Mkfifo(fifo, 0o600))
				skill := filepath.Join(root, "skill")
				require.NoError(t, os.Mkdir(skill, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("synthetic"), 0o600))
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusBadRequest)
				}))
				defer server.Close()
				binary, err := os.Executable()
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				args := []string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "skills", "create", "--files", fifo}
				var input io.Reader
				if mode == "generic body reference" {
					args = args[:len(args)-2]
					body, err := json.Marshal(map[string]any{"files": []string{skill}, "note": "@file://" + fifo})
					require.NoError(t, err)
					input = bytes.NewReader(body)
				}
				child := exec.CommandContext(ctx, binary, args...)
				for _, entry := range os.Environ() {
					name, _, _ := strings.Cut(entry, "=")
					if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
						child.Env = append(child.Env, entry)
					}
				}
				child.Env = append(child.Env, "HOME="+home, "XDG_CONFIG_HOME="+home,
					"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=synthetic-skill-key",
					"OPENAI_BASE_URL="+server.URL, "TMPDIR="+scratch, "NO_COLOR=1")
				child.Stdin = input
				var stdout, stderr bytes.Buffer
				child.Stdout, child.Stderr = &stdout, &stderr
				require.NoError(t, child.Start())
				done := make(chan error, 1)
				go func() { done <- child.Wait() }()
				waited := false
				defer func() {
					cancel()
					if !waited {
						<-done
					}
				}()
				// There is no writer. The existing FIFO open must stay interruptible.
				select {
				case err := <-done:
					waited = true
					t.Fatalf("command exited before the signal: %v; %s", err, stderr.String())
				case <-time.After(300 * time.Millisecond):
				}
				entries, err := os.ReadDir(scratch)
				require.NoError(t, err)
				require.Empty(t, entries, "generic embedding must precede archive creation")
				require.NoError(t, child.Process.Signal(processSignal))
				select {
				case err := <-done:
					waited = true
					var exit *exec.ExitError
					require.ErrorAs(t, err, &exit)
					status, ok := exit.Sys().(syscall.WaitStatus)
					require.True(t, ok)
					require.True(t, status.Signaled(), "stderr=%s", stderr.String())
					require.Equal(t, exitCode, 128+int(status.Signal()))
				case <-time.After(2 * time.Second):
					t.Fatal("first signal did not stop a blocked Skills input")
				}
				require.Empty(t, stdout.String())
				require.Zero(t, requests.Load(), "no HTTP handler may start before FIFO input opens")
			})
		}
	})
}

func TestMainSkillUploadFIFOBytesAndLiteralHeader(t *testing.T) {
	for _, mode := range []string{"file input", "generic body reference"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			fifo := filepath.Join(root, "source.fifo")
			require.NoError(t, unix.Mkfifo(fifo, 0o600))
			payload := []byte("@unchanged text\n\x00\xff")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			producer := exec.CommandContext(ctx, "/bin/sh", "-c", `cat "$1" > "$2"`, "producer", filepath.Join(root, "source.bin"), fifo)
			require.NoError(t, os.WriteFile(filepath.Join(root, "source.bin"), payload, 0o600))
			require.NoError(t, producer.Start())
			defer func() { cancel(); _ = producer.Wait() }()
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			literalHeader := "@file://" + fifo
			args := []string{"skills", "create", "--files", fifo, "--header", "X-Skill-Name:" + literalHeader}
			var input string
			field := "files[]"
			if mode == "generic body reference" {
				skill := filepath.Join(root, "skill")
				require.NoError(t, os.Mkdir(skill, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("synthetic"), 0o600))
				body, err := json.Marshal(map[string]any{"files": []string{skill}, "note": "@file://" + fifo})
				require.NoError(t, err)
				input = string(body)
				args = []string{"skills", "create", "--header", "X-Skill-Name:" + literalHeader}
				field = "note"
			}
			got := runShellFileCommand(t, server, shellFileInput(t, []byte(input)), nil, args...)
			require.Zero(t, got.code, "%+v", got)
			require.Len(t, requests(), 1)
			request := requests()[0]
			require.Equal(t, literalHeader, request.header.Get("X-Skill-Name"))
			var received []byte
			for _, part := range request.parts {
				if part.name == field {
					received = part.data
				}
			}
			require.Equal(t, payload, received)
		})
	}
}
