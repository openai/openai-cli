//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func newImageReceiptProcess(t *testing.T, ctx context.Context, binary string, env []string, args ...string) (*exec.Cmd, func(error) error) {
	t.Helper()
	ackPath := filepath.Join(t.TempDir(), "stdout-pipe.pid")
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainImageReceiptWindowsProcess$", "--"}, args...)...)
	child.Env = append(env, "OPENAI_CLI_IMAGE_RECEIPT_PROCESS=1", "OPENAI_CLI_IMAGE_RECEIPT_ACK="+ackPath)
	// Avoid console startup replacing the inherited anonymous pipe.
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return child, func(runErr error) error {
		ack, err := os.ReadFile(ackPath)
		if err != nil {
			return fmt.Errorf("read stdout pipe acknowledgement: %w", err)
		}
		if child.Process == nil || string(ack) != strconv.Itoa(child.Process.Pid) {
			return fmt.Errorf("stdout pipe acknowledgement does not identify the child: %q", ack)
		}
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("receipt failure exit = %v, want 1", runErr)
		}
		return nil
	}
}

func TestMainImageReceiptWindowsProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_IMAGE_RECEIPT_PROCESS") != "1" {
		return
	}
	fail := func(message string) {
		fmt.Fprintln(os.Stderr, "image receipt fixture: "+message)
		os.Exit(97)
	}
	handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		fail("cannot inspect stdout")
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil || fileType != windows.FILE_TYPE_PIPE || handle != windows.Handle(os.Stdout.Fd()) {
		fail("stdout is not the inherited pipe")
	}
	ack, err := os.OpenFile(os.Getenv("OPENAI_CLI_IMAGE_RECEIPT_ACK"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fail("cannot create stdout pipe acknowledgement")
	}
	_, writeErr := fmt.Fprint(ack, os.Getpid())
	closeErr := ack.Close()
	if writeErr != nil || closeErr != nil {
		fail("cannot write stdout pipe acknowledgement")
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		fail("missing subprocess argument separator")
	}
	os.Args = os.Args[separator+1:]
	main()
	os.Exit(0)
}

func TestImageReceiptProcessRejectsNonPipeStdout(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := os.CreateTemp(t.TempDir(), "stdout-")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child, verifyFixture := newImageReceiptProcess(t, ctx, binary, imageGenerationEnv(server, t.TempDir()),
		"openai", "images", "generate", "--prompt", "synthetic image", "--output-dir", t.TempDir())
	var stderr bytes.Buffer
	child.Stdout, child.Stderr = stdout, &stderr
	runErr := child.Run()
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(runErr, &exit) || exit.ExitCode() != 97 {
		t.Fatalf("non-pipe fixture was not rejected: %v; stderr=%q", runErr, stderr.String())
	}
	if !strings.Contains(stderr.String(), "image receipt fixture: stdout is not the inherited pipe") {
		t.Fatalf("missing fixture failure: %q", stderr.String())
	}
	if err := verifyFixture(runErr); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-pipe fixture wrote an acknowledgement: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("non-pipe fixture called the API: %d requests", requests.Load())
	}
}
