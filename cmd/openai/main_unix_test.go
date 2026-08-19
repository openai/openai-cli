//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	appcmd "github.com/openai/openai-cli/pkg/cmd"
	"github.com/urfave/cli/v3"
)

const interruptedDownloadHelper = "OPENAI_INTERRUPTED_DOWNLOAD_HELPER"
const blockedSignalHelper = "OPENAI_BLOCKED_SIGNAL_HELPER"

func TestMainCleansInterruptedDownloads(t *testing.T) {
	for _, interrupted := range []struct {
		name   string
		signal os.Signal
	}{
		{name: "SIGINT", signal: os.Interrupt},
		{name: "SIGTERM", signal: syscall.SIGTERM},
	} {
		t.Run(interrupted.name, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				if _, err := w.Write(bytes.Repeat([]byte("sensitive response "), 1024)); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				close(started)
				<-request.Context().Done()
			}))
			t.Cleanup(server.Close)

			directory := t.TempDir()
			outfile := filepath.Join(directory, "download.bin")
			if err := os.WriteFile(outfile, []byte("original content"), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatalf("os.Executable() = %v, want nil", err)
			}
			process := exec.CommandContext(t.Context(), executable, "-test.run=^TestInterruptedDownloadHelper$")
			process.Env = append(os.Environ(),
				interruptedDownloadHelper+"=1",
				"OPENAI_INTERRUPTED_DOWNLOAD_BASE_URL="+server.URL+"/",
				"OPENAI_INTERRUPTED_DOWNLOAD_OUTPUT="+outfile,
			)
			var output bytes.Buffer
			process.Stdout = &output
			process.Stderr = &output
			if err := process.Start(); err != nil {
				t.Fatalf("interrupted download subprocess Start() = %v, want nil", err)
			}

			select {
			case <-started:
			case <-time.After(10 * time.Second):
				_ = process.Process.Kill()
				_ = process.Wait()
				t.Fatal("interrupted download subprocess did not receive its first response chunk")
			}

			deadline := time.Now().Add(5 * time.Second)
			for {
				entries, readErr := os.ReadDir(directory)
				content, contentErr := os.ReadFile(outfile)
				if readErr == nil && (len(entries) > 1 || (contentErr == nil && string(content) != "original content")) {
					break
				}
				if time.Now().After(deadline) {
					_ = process.Process.Kill()
					_ = process.Wait()
					t.Fatal("interrupted download subprocess did not begin writing its response")
				}
				time.Sleep(10 * time.Millisecond)
			}

			if err := process.Process.Signal(interrupted.signal); err != nil {
				_ = process.Process.Kill()
				_ = process.Wait()
				t.Fatalf("interrupted download Signal(%s) = %v, want nil", interrupted.name, err)
			}
			if err := process.Wait(); err == nil {
				t.Errorf("interrupted download Wait(%s) = nil, want a nonzero exit", interrupted.name)
			}

			content, err := os.ReadFile(outfile)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) = %v, want original content", outfile, err)
			}
			if string(content) != "original content" {
				t.Errorf("interrupted download %s output length = %d, want unchanged %q; subprocess: %s", interrupted.name, len(content), "original content", output.String())
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatalf("os.ReadDir(%q) = %v, want nil", directory, err)
			}
			if len(entries) != 1 || entries[0].Name() != "download.bin" {
				t.Errorf("interrupted download %s left entries %v, want only the original destination", interrupted.name, entries)
			}
		})
	}
}

func TestInterruptedDownloadHelper(t *testing.T) {
	if os.Getenv(interruptedDownloadHelper) != "1" {
		t.Skip("interrupted download helper subprocess")
	}
	os.Args = []string{
		"openai",
		"--base-url", os.Getenv("OPENAI_INTERRUPTED_DOWNLOAD_BASE_URL"),
		"--api-key", "synthetic-test-key",
		"files", "content", "--file-id", "file_123",
		"--output", os.Getenv("OPENAI_INTERRUPTED_DOWNLOAD_OUTPUT"),
	}
	main()
}

func TestMainSecondSignalTerminatesBlockedOperation(t *testing.T) {
	for _, interrupted := range []struct {
		name   string
		signal os.Signal
	}{
		{name: "SIGINT", signal: os.Interrupt},
		{name: "SIGTERM", signal: syscall.SIGTERM},
	} {
		t.Run(interrupted.name, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatalf("os.Executable() = %v, want nil", err)
			}
			process := exec.CommandContext(t.Context(), executable, "-test.run=^TestBlockedSignalHelper$")
			process.Env = append(os.Environ(), blockedSignalHelper+"=1")
			stdout, err := process.StdoutPipe()
			if err != nil {
				t.Fatalf("blocked-signal subprocess StdoutPipe() = %v, want nil", err)
			}
			var stderr bytes.Buffer
			process.Stderr = &stderr
			if err := process.Start(); err != nil {
				t.Fatalf("blocked-signal subprocess Start() = %v, want nil", err)
			}
			events := make(chan string, 2)
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					events <- scanner.Text()
				}
			}()
			waitForSignalEvent := func(want string) {
				t.Helper()
				select {
				case got := <-events:
					if got != want {
						t.Fatalf("blocked-signal subprocess event = %q, want %q", got, want)
					}
				case <-time.After(5 * time.Second):
					_ = process.Process.Kill()
					_ = process.Wait()
					t.Fatalf("blocked-signal subprocess did not report %q", want)
				}
			}
			waitForSignalEvent("ready")
			if err := process.Process.Signal(interrupted.signal); err != nil {
				t.Fatalf("first Signal(%s) = %v, want nil", interrupted.name, err)
			}
			waitForSignalEvent("canceled")
			if err := process.Process.Signal(interrupted.signal); err != nil {
				t.Fatalf("second Signal(%s) = %v, want nil", interrupted.name, err)
			}
			exited := make(chan error, 1)
			go func() { exited <- process.Wait() }()
			select {
			case err := <-exited:
				if err == nil {
					t.Errorf("second Signal(%s) exit = nil, want signal termination", interrupted.name)
				}
			case <-time.After(2 * time.Second):
				_ = process.Process.Kill()
				<-exited
				t.Errorf("second Signal(%s) did not terminate the blocked CLI; stderr: %s", interrupted.name, stderr.String())
			}
		})
	}
}

func TestBlockedSignalHelper(t *testing.T) {
	if os.Getenv(blockedSignalHelper) != "1" {
		t.Skip("blocked-signal helper subprocess")
	}
	appcmd.Command = &cli.Command{
		Name: "openai",
		Action: func(ctx context.Context, command *cli.Command) error {
			fmt.Fprintln(os.Stdout, "ready")
			<-ctx.Done()
			fmt.Fprintln(os.Stdout, "canceled")
			select {}
		},
	}
	os.Args = []string{"openai"}
	main()
}
