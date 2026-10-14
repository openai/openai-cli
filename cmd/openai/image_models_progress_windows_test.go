//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

func TestImageModelsWindowsStderrProgress(t *testing.T) {
	home := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	var responses atomic.Int32
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			responses.Add(1)
			mainImageModelResponse(w, r.URL.Path, "null")
		case <-r.Context().Done():
		}
	})
	// Register after the server cleanup, so a failed assertion releases its handlers first.
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		server.CloseClientConnections()
	})
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, binary, "-test.run=^TestShellFileWindowsConsoleHelper$", "--", binary,
		"-test.run=^TestImageModelsWindowsStderrChild$", "--", "openai", "--base-url", server.URL,
		"--api-key", "synthetic-models-key", "--format", "text", "images", "models")
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	process.WaitDelay = 2 * time.Second
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			process.Env = append(process.Env, name+"="+value)
		}
	}
	process.Env = append(process.Env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home,
		"LOCALAPPDATA="+home, "TEMP="+home, "TMP="+home, "NO_COLOR=1", "GOMAXPROCS=2",
		"OPENAI_CLI_IMAGE_MODELS_STDERR_CHILD=1")
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	input, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		input.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		input.Close()
		<-done // The console owner's kill-on-close job contains its child.
	})
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("console child exited before metadata request: %v; stdout=%q stderr=%q", err, &stdout, &stderr)
	case <-ctx.Done():
		t.Fatal("metadata request did not start")
	}
	deadline := time.NewTimer(7 * time.Second)
	defer deadline.Stop()
	for {
		observed, err := os.ReadFile(filepath.Join(home, "progress-observed"))
		if err == nil {
			if string(observed) != "Checking image models...\n" || responses.Load() != 0 {
				t.Fatalf("progress did not precede held responses: %q; responses=%d", observed, responses.Load())
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatalf("console child exited before progress: %v; stdout=%q stderr=%q", err, &stdout, &stderr)
		case <-deadline.C:
			t.Fatal("stderr console did not display progress before the response")
		case <-time.After(10 * time.Millisecond):
		}
	}
	releaseOnce.Do(func() { close(release) })
	err = <-done
	// EOF on this pipe instructs the controller to cancel. Keep it open until the child exits.
	closeErr := input.Close()
	if err != nil || (closeErr != nil && !errors.Is(closeErr, os.ErrClosed)) || ctx.Err() != nil || stderr.Len() != 0 {
		t.Fatalf("console discovery failed: %v; input close=%v; context=%v; stdout=%q stderr=%q", err, closeErr, ctx.Err(), &stdout, &stderr)
	}
	const want = "Image models — checked with your API key\n\n" +
		"MODEL                   STATUS\n" +
		"gpt-image-2.5-sunburst  Visible\n" +
		"gpt-image-2.5-flare     Visible\n" +
		"gpt-image-2             Visible\n" +
		"gpt-image-1.5           Visible\n" +
		"gpt-image-1             Visible\n" +
		"gpt-image-1-mini        Visible\n" +
		"chatgpt-image-latest    Visible\n" +
		"dall-e-3                Visible\n" +
		"dall-e-2                Visible\n\n" +
		"Known image IDs from this CLI; newly released models may not be listed.\n" +
		"Visible means model information was accessible; generation permissions can differ.\n"
	if stdout.String() != want {
		t.Fatalf("piped report changed: got %q; want %q", &stdout, want)
	}
	assertMainImageModelsRoutes(t, requests(), false)
	if responses.Load() != 9 {
		t.Fatalf("completed metadata responses=%d; want 9", responses.Load())
	}
}

// The existing console owner runs production main with only stderr attached to its console.
func TestImageModelsWindowsStderrChild(t *testing.T) {
	if os.Getenv("OPENAI_CLI_IMAGE_MODELS_STDERR_CHILD") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing image model command")
	}
	console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	stdoutType, err := windows.GetFileType(windows.Handle(os.Stdout.Fd()))
	if err != nil || stdoutType != windows.FILE_TYPE_PIPE || term.IsTerminal(os.Stdin.Fd()) ||
		term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(console.Fd()) {
		t.Fatalf("expected only stderr on a real console: stdout type=%d; error=%v", stdoutType, err)
	}
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(console.Fd())); err != nil {
		t.Fatal(err)
	}
	os.Stderr = console
	observed := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			screen, err := readOutputConsole(console)
			if err != nil {
				observed <- err
				return
			}
			if strings.Contains(screen, "Checking image models...") {
				path := filepath.Join(os.Getenv("HOME"), "progress-observed")
				file, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					observed <- err
					return
				}
				_, writeErr := file.WriteString("Checking image models...\n")
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					observed <- errors.Join(writeErr, closeErr)
					return
				}
				observed <- os.Rename(path+".tmp", path)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		observed <- errors.New("progress was absent from the stderr console")
	}()
	os.Args = os.Args[separator+1:]
	main()
	if err := <-observed; err != nil {
		fmt.Fprintln(os.Stdout, err)
		os.Exit(125)
	}
	if err := console.Close(); err != nil {
		fmt.Fprintln(os.Stdout, err)
		os.Exit(125)
	}
	os.Exit(0)
}
