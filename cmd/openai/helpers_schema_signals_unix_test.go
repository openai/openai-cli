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
			output := schemaSignalBlockedOutput(t)
			child := schemaSignalCommand(t, server.URL, path, "text")
			child.Stdout, child.Stderr = io.Discard, output.writer
			process := startSchemaSignalProcess(t, child)
			waitForSchemaSignalOutputBlock(t, process, output.writer)
			require.Zero(t, requests.Load(), "blocked preflight must not send a paid request")
			requireSchemaSignalDirectory(t, directory)
			interruptSchemaSignalProcess(t, process, signal)
			output.requirePartialModel(t)
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
			output := schemaSignalBlockedOutput(t)
			child := schemaSignalCommand(t, server.URL, path, "json")
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = output.writer, &stderr
			process := startSchemaSignalProcess(t, child)
			select {
			case err := <-responseWritten:
				require.NoError(t, err)
			case <-process.done:
				t.Fatalf("schema command exited before the response: %v; stderr=%q", process.err, stderr.String())
			case <-time.After(10 * time.Second):
				t.Fatal("schema command did not reach the synthetic service")
			}
			waitForSchemaSignalOutputBlock(t, process, output.writer)
			data, err := os.ReadFile(path)
			require.NoError(t, err, "receipt output must follow publication")
			require.Equal(t, helperSchemaFixture, string(data))
			requireSchemaSignalDirectory(t, directory, "schema.json")
			interruptSchemaSignalProcess(t, process, signal)
			output.requirePartialModel(t)
			require.EqualValues(t, 1, requests.Load())
			require.Empty(t, stderr.String())
			data, err = os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, helperSchemaFixture, string(data))
			requireSchemaSignalDirectory(t, directory, "schema.json")
		})
	}
}

const schemaSignalModelStart = "schema-signal-model-start-"
const schemaSignalModelEnd = "-schema-signal-model-end"
const schemaSignalModelPadding = 96 * 1024

type schemaSignalOutput struct {
	reader, writer *os.File
	prefilled      int
	remaining      int
	writerClosed   bool
}

// A socketpair gives these os.File writes bounded buffers and observable
// backpressure. Darwin pipe readiness can overstate a small pipe's capacity.
func schemaSignalBlockedOutput(t *testing.T) *schemaSignalOutput {
	t.Helper()
	syscall.ForkLock.RLock()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(fds[0])
		unix.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	require.NoError(t, err)
	if err := unix.SetNonblock(fds[0], true); err != nil {
		_ = unix.Close(fds[0])
		_ = unix.Close(fds[1])
		t.Fatalf("prepare deadline-capable output reader: %v", err)
	}
	reader := os.NewFile(uintptr(fds[0]), "schema-signal-reader")
	writer := os.NewFile(uintptr(fds[1]), "schema-signal-writer")
	output := &schemaSignalOutput{reader: reader, writer: writer}
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
		if !output.writerClosed {
			require.NoError(t, writer.Close())
		}
	})
	require.NoError(t, reader.SetReadDeadline(time.Now().Add(3*time.Second)))
	require.NoError(t, unix.SetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_RCVBUF, 1024))
	require.NoError(t, unix.SetsockoptInt(fds[1], unix.SOL_SOCKET, unix.SO_SNDBUF, 1024))
	receiveBuffer, err := unix.GetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_RCVBUF)
	require.NoError(t, err)
	sendBuffer, err := unix.GetsockoptInt(fds[1], unix.SOL_SOCKET, unix.SO_SNDBUF)
	require.NoError(t, err)
	require.Less(t, sendBuffer+receiveBuffer, schemaSignalModelPadding, "model output must exceed the effective socket buffers")
	fd := fds[1]
	require.NoError(t, unix.SetNonblock(fd, true))
	// Measure the actual buffered bytes; kernel buffer minima vary by platform.
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
		require.Positive(t, n, "prefill must make progress")
		require.Less(t, total, 8*1024*1024, "unexpectedly large output buffer")
	}
	require.NoError(t, unix.SetNonblock(fd, false))
	require.Positive(t, total)
	require.False(t, schemaSignalOutputWritable(t, writer), "prefill must establish actual backpressure")
	output.prefilled, output.remaining = total, total
	for !schemaSignalOutputWritable(t, writer) {
		require.Positive(t, output.remaining, "empty output buffer must become writable")
		size := min(512, output.remaining)
		_, err := io.ReadFull(reader, make([]byte, size))
		require.NoError(t, err)
		output.remaining -= size
	}
	t.Logf("output buffer: send=%d receive=%d prefilled=%d retained=%d", sendBuffer, receiveBuffer, total, output.remaining)
	return output
}

func (output *schemaSignalOutput) requirePartialModel(t *testing.T) {
	t.Helper()
	output.writerClosed = true
	require.NoError(t, output.writer.Close())
	require.NoError(t, output.reader.SetReadDeadline(time.Now().Add(3*time.Second)))
	data, err := io.ReadAll(output.reader)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), output.remaining)
	require.True(t, bytes.Equal(data[:output.remaining], bytes.Repeat([]byte("f"), output.remaining)), "fixture prefix changed")
	actual := string(data[output.remaining:])
	require.Contains(t, actual, schemaSignalModelStart, "the CLI must reach the blocked output write")
	require.NotContains(t, actual, schemaSignalModelEnd, "the CLI must not finish model output before interruption")
	t.Logf("partial CLI output: bytes=%d prefilled=%d retained=%d", len(actual), output.prefilled, output.remaining)
}

func schemaSignalOutputWritable(t *testing.T, writer *os.File) bool {
	t.Helper()
	fd := int(writer.Fd())
	var writable unix.FdSet
	writable.Set(fd)
	for {
		count, err := unix.Select(fd+1, nil, &writable, nil, &unix.Timeval{})
		if errors.Is(err, unix.EINTR) {
			writable.Set(fd)
			continue
		}
		require.NoError(t, err)
		return count != 0
	}
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
		"--model", schemaSignalModelStart + strings.Repeat("x", schemaSignalModelPadding) + schemaSignalModelEnd, "--output", output,
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

func waitForSchemaSignalOutputBlock(t *testing.T, process *schemaSignalProcess, writer *os.File) {
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
		if !schemaSignalOutputWritable(t, writer) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("schema command did not consume the available output capacity")
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
