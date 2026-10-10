//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
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

func TestMainSchemaHelperSignalWhilePreflightStderrBlocked(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, helperSchemaResponse(helperSchemaFixture))
			}))
			defer server.Close()
			directory := t.TempDir()
			path := filepath.Join(directory, "schema.json")
			pipe := schemaSignalBlockedPipe(t)
			child := schemaSignalCommand(t, server.URL, path, "text")
			child.Stdout, child.Stderr = io.Discard, pipe
			process := startSchemaSignalProcess(t, child)
			waitForSchemaSignalPipeBlock(t, process, pipe)
			require.Zero(t, requests.Load(), "blocked preflight must not send a paid request")
			requireSchemaSignalDirectory(t, directory)
			interruptSchemaSignalProcess(t, process, signal)
			require.Zero(t, requests.Load())
			requireSchemaSignalDirectory(t, directory)
		})
	}
}

func TestMainSchemaHelperSignalWhileSavedReceiptStdoutBlocked(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			var requests atomic.Int32
			responseWritten := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, err := io.WriteString(w, helperSchemaResponse(helperSchemaFixture))
				select {
				case responseWritten <- err:
				default:
				}
			}))
			defer server.Close()
			directory := t.TempDir()
			path := filepath.Join(directory, "schema.json")
			pipe := schemaSignalBlockedPipe(t)
			child := schemaSignalCommand(t, server.URL, path, "json")
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = pipe, &stderr
			process := startSchemaSignalProcess(t, child)
			select {
			case err := <-responseWritten:
				require.NoError(t, err)
			case <-process.done:
				t.Fatalf("schema command exited before the response: %v; stderr=%q", process.err, stderr.String())
			case <-time.After(10 * time.Second):
				t.Fatal("schema command did not reach the synthetic service")
			}
			waitForSchemaSignalPipeBlock(t, process, pipe)
			data, err := os.ReadFile(path)
			require.NoError(t, err, "receipt output must follow publication")
			require.Equal(t, helperSchemaFixture, string(data))
			requireSchemaSignalDirectory(t, directory, "schema.json")
			interruptSchemaSignalProcess(t, process, signal)
			require.EqualValues(t, 1, requests.Load())
			require.Empty(t, stderr.String())
			data, err = os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, helperSchemaFixture, string(data))
			requireSchemaSignalDirectory(t, directory, "schema.json")
		})
	}
}

// Fill the pipe before launch, then free one writable slot. The child has a
// larger output, so loss of writability proves that its write reached the pipe.
func schemaSignalBlockedPipe(t *testing.T) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
		require.NoError(t, writer.Close())
	})
	fd := int(writer.Fd())
	require.NoError(t, unix.SetNonblock(fd, true))
	// A large initial write also exercises adaptive pipe capacity before launch.
	filler := bytes.Repeat([]byte("f"), 128*1024)
	total := 0
	for {
		n, err := unix.Write(fd, filler)
		if n > 0 {
			total += n
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			break
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		require.NoError(t, err)
		require.Less(t, total, 8*1024*1024, "unexpectedly large pipe capacity")
	}
	require.NoError(t, unix.SetNonblock(fd, false))
	require.GreaterOrEqual(t, total, 4096)
	_, err = io.ReadFull(reader, make([]byte, 4096))
	require.NoError(t, err)
	require.True(t, schemaSignalPipeWritable(t, writer), "readiness needs one writable slot")
	return writer
}

func schemaSignalPipeWritable(t *testing.T, writer *os.File) bool {
	t.Helper()
	fd := int(writer.Fd())
	var writable unix.FdSet
	writable.Set(fd)
	count, err := unix.Select(fd+1, nil, &writable, nil, &unix.Timeval{})
	require.NoError(t, err)
	return count != 0
}

func schemaSignalCommand(t *testing.T, endpoint, output, format string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	args := []string{
		"-test.run=^TestMainDispatchProcess$", "--", "openai",
		"--base-url", endpoint, "--api-key", "sk-fake-schema-signals",
		"--format", format, "helpers", "schema", "--description", "An invoice",
		"--model", strings.Repeat("synthetic-model-", 2300), "--output", output,
	}
	child := exec.CommandContext(ctx, executable, args...)
	home := t.TempDir()
	child.Env = []string{
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "HOME=" + home, "XDG_CONFIG_HOME=" + home,
		"PATH=/usr/bin:/bin", "FORCE_COLOR=0", "NO_COLOR=1", "GOMAXPROCS=2",
	}
	child.WaitDelay = time.Second
	return child
}

type schemaSignalProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
}

func startSchemaSignalProcess(t *testing.T, command *exec.Cmd) *schemaSignalProcess {
	t.Helper()
	require.NoError(t, command.Start())
	process := &schemaSignalProcess{command: command, done: make(chan struct{})}
	go func() {
		process.err = command.Wait()
		close(process.done)
	}()
	t.Cleanup(func() {
		select {
		case <-process.done:
			return
		default:
			_ = command.Process.Kill()
		}
		select {
		case <-process.done:
		case <-time.After(3 * time.Second):
			t.Error("owned schema command did not exit after cleanup kill")
		}
	})
	return process
}

func waitForSchemaSignalPipeBlock(t *testing.T, process *schemaSignalProcess, writer *os.File) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-process.done:
			t.Fatalf("schema command exited before blocking output: %v", process.err)
		default:
		}
		if !schemaSignalPipeWritable(t, writer) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("schema command did not fill the writable pipe slot")
		case <-tick.C:
		}
	}
}

func interruptSchemaSignalProcess(t *testing.T, process *schemaSignalProcess, signal syscall.Signal) {
	t.Helper()
	require.NoError(t, process.command.Process.Signal(signal))
	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		t.Fatal("one signal did not stop the schema command within three seconds")
	}
	require.Error(t, process.err)
	status, ok := process.command.ProcessState.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	if status.Signaled() {
		require.Equal(t, signal, status.Signal())
	} else {
		require.Equal(t, 128+int(signal), status.ExitStatus())
	}
}

func requireSchemaSignalDirectory(t *testing.T, directory string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	require.ElementsMatch(t, want, names)
}
