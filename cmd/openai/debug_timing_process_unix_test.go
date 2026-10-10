//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMainDebugTimingBlockedStderrDownloadCleanup(t *testing.T) {
	for _, interrupts := range []int{1, 2} {
		t.Run(fmt.Sprintf("interrupts=%d", interrupts), func(t *testing.T) {
			const prefix = "synthetic first download bytes"
			const original = "synthetic prior destination"
			release, disconnected := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseBody := func() { releaseOnce.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(disconnected)
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", "4096")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)
			t.Cleanup(server.CloseClientConnections)
			t.Cleanup(releaseBody)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reader.Close(); writer.Close() })
			home := t.TempDir()
			destination := filepath.Join(home, "result.bin")
			if err := os.WriteFile(destination, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai",
				"--debug", "--quiet", "files", "content", "file_synthetic", "--output", destination)
			child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-blocked-timing",
				"OPENAI_BASE_URL=" + server.URL, "HOME=" + home, "USERPROFILE=" + home,
				"XDG_CONFIG_HOME=" + home, "GOMAXPROCS=2", "FORCE_COLOR=0"}
			var stdout bytes.Buffer
			child.Stdout, child.Stderr = &stdout, writer
			child.WaitDelay = time.Second
			if err := child.Start(); err != nil {
				cancel()
				t.Fatal(err)
			}
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = child.Wait(); close(done) }()
			t.Cleanup(func() {
				cancel()
				_ = child.Process.Kill()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("could not reap the canceled download process")
				}
			})

			// Drain the real header dump before blocking diagnostics. The server
			// cannot send body bytes until the managed download stage exists.
			if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var headers bytes.Buffer
			var one [1]byte
			for !bytes.Contains(headers.Bytes(), []byte("Response Content:\n")) || !bytes.HasSuffix(headers.Bytes(), []byte("\r\n\r\n\n")) {
				if _, err := io.ReadFull(reader, one[:]); err != nil {
					t.Fatalf("read response header dump: %v; captured=%q", err, headers.String())
				}
				headers.WriteByte(one[0])
			}
			if err := reader.SetReadDeadline(time.Time{}); err != nil {
				t.Fatal(err)
			}
			waitDebugTimingDownloadStages(t, done, home, "managed stage creation", func(paths []string) bool { return len(paths) == 1 })
			fillDebugTimingStderrPipe(t, writer)
			releaseBody()
			waitDebugTimingDownloadStages(t, done, home, "first body bytes while stderr stays full", func(paths []string) bool {
				if len(paths) != 1 {
					return false
				}
				data, err := os.ReadFile(paths[0])
				return err == nil && string(data) == prefix
			})
			if err := child.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			waitDebugTimingDownloadStages(t, done, home, "stage cleanup before stderr drains", func(paths []string) bool { return len(paths) == 0 })
			select {
			case <-disconnected:
			case <-time.After(5 * time.Second):
				t.Fatal("interrupted download retained its HTTP response")
			}
			if data, err := os.ReadFile(destination); err != nil || string(data) != original {
				t.Fatalf("interruption changed the existing destination: bytes=%q error=%v", data, err)
			}
			select {
			case <-done:
				t.Fatalf("process exited before blocked diagnostics drained: %v", waitErr)
			default:
			}
			if interrupts == 2 {
				if err := child.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("second interrupt did not stop the blocked process")
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			type drainResult struct {
				data []byte
				err  error
			}
			drained := make(chan drainResult, 1)
			go func() { data, err := io.ReadAll(reader); drained <- drainResult{data, err} }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("interrupted download did not exit after draining stderr")
			}
			if interrupts == 1 {
				if waitErr == nil || child.ProcessState.ExitCode() != 130 {
					t.Fatalf("first interrupt lost status 130: %v", waitErr)
				}
			} else if status, ok := child.ProcessState.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
				t.Fatalf("second interrupt lost SIGINT status: %v", waitErr)
			}
			select {
			case result := <-drained:
				if result.err != nil {
					t.Fatal(result.err)
				}
				log := headers.String() + string(result.data)
				if interrupts == 1 {
					assertDebugTimingStage(t, log, 1, "first response data read", "")
				}
				if strings.Contains(log, "response body fully consumed") || strings.Contains(log, prefix) || strings.Contains(log, "sk-fake-blocked-timing") {
					t.Fatalf("incomplete download diagnostics misreported completion or disclosed data: %q", log)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stderr reader remained blocked after process exit")
			}
			if stdout.Len() != 0 {
				t.Fatalf("download wrote to stdout: %q", stdout.String())
			}
			assertShellCompletionStagesAbsent(t, home)
			if data, err := os.ReadFile(destination); err != nil || string(data) != original {
				t.Fatalf("final cleanup changed the existing destination: bytes=%q error=%v", data, err)
			}
		})
	}
}

func waitDebugTimingDownloadStages(t *testing.T, done <-chan struct{}, directory, stage string, ready func([]string) bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		paths, err := filepath.Glob(filepath.Join(directory, ".openai-download-*.tmp"))
		if err != nil {
			t.Fatal(err)
		}
		if ready(paths) {
			return
		}
		select {
		case <-done:
			t.Fatalf("process exited before %s", stage)
		case <-deadline.C:
			t.Fatalf("did not observe %s; stages=%v", stage, paths)
		case <-poll.C:
		}
	}
}

func fillDebugTimingStderrPipe(t *testing.T, writer *os.File) {
	t.Helper()
	fd := writer.Fd()
	flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK != 0 {
		t.Fatal("fixture stderr pipe must start in blocking mode")
	}
	if err := unix.SetNonblock(int(fd), true); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := unix.FcntlInt(fd, unix.F_SETFL, flags); err != nil {
			t.Fatalf("restore fixture stderr pipe flags: %v", err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for _, fill := range [][]byte{bytes.Repeat([]byte("p"), 4096), []byte("p")} {
		for {
			if time.Now().After(deadline) {
				t.Fatal("could not fill fixture stderr pipe")
			}
			n, err := unix.Write(int(fd), fill)
			if errors.Is(err, unix.EAGAIN) {
				break
			}
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil || n == 0 {
				t.Fatalf("fill fixture stderr pipe: bytes=%d error=%v", n, err)
			}
		}
	}
}
