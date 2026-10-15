//go:build windows

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func setFiniteJSONListOutputProcess(child *exec.Cmd) {
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

func TestMainDispatchFiniteJSONListCancellationPreservesPartialOutput(t *testing.T) {
	var requests atomic.Int32
	requested, disconnected, fixtureRelease := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		after := r.URL.Query().Get("after")
		validCursor := request == 1 && after == "" || request == 2 && after == "file_a"
		if r.Method != http.MethodGet || r.URL.Path != "/files" || !validCursor {
			t.Errorf("unexpected list request %d: %s %s", request, r.Method, r.URL)
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if request == 1 {
			io.WriteString(w, resourceFilePage([]string{"file_a"}, true))
			return
		}
		close(requested)
		select {
		case <-r.Context().Done():
			close(disconnected)
		case <-fixtureRelease:
			// Failure cleanup must not satisfy the disconnect assertion.
		}
	}))
	t.Cleanup(func() {
		close(fixtureRelease)
		server.CloseClientConnections()
		server.Close()
	})
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "OPENAI_API_KEY=sk-fake-list-test",
		"OPENAI_BASE_URL=" + server.URL, "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb"}
	child, interrupt, verifyFixture := imageEditInterruptCommand(t, ctx, binary, home, env,
		"openai", "--format", "json", "files", "list")
	child.WaitDelay = 2 * time.Second
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr adminConsoleCapture
	child.Stdout, child.Stderr = writer, &stderr
	started, draining := false, false
	waitDone, drainDone := make(chan struct{}), make(chan struct{})
	var waitErr, drainErr error
	t.Cleanup(func() {
		cancel()
		writer.Close()
		if started {
			select {
			case <-waitDone:
			case <-time.After(4 * time.Second):
				child.Process.Kill()
				select {
				case <-waitDone:
				case <-time.After(3 * time.Second):
					t.Error("console helper did not join during cleanup")
				}
			}
		}
		reader.Close()
		if draining {
			select {
			case <-drainDone:
			case <-time.After(3 * time.Second):
				t.Error("list capture did not join during cleanup")
			}
		}
	})
	startedAt := time.Now()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	started = true
	go func() { waitErr = child.Wait(); close(waitDone) }()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	firstCtx, stopFirst := context.WithTimeout(ctx, 5*time.Second)
	defer stopFirst()
	readFiniteJSONListFirst(t, firstCtx, io.TeeReader(reader, &stdout))
	select {
	case <-requested:
	case <-waitDone:
		t.Fatalf("console child exited before the next page: %v", waitErr)
	case <-firstCtx.Done():
		t.Fatal("next page was not requested")
	}
	stopFirst()
	select {
	case <-waitDone:
		t.Fatalf("console child exited before control delivery: %v", waitErr)
	case <-disconnected:
		t.Fatal("HTTP source closed before control delivery")
	default:
	}
	draining = true
	go func() { _, drainErr = io.Copy(&stdout, reader); close(drainDone) }()
	if err := interrupt(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waitDone:
	case <-ctx.Done():
		t.Fatal("console helper did not exit after control delivery")
	}
	elapsed := time.Since(startedAt)
	select {
	case <-drainDone:
	case <-ctx.Done():
		t.Fatal("list capture remained open after console exit")
	}
	if drainErr != nil || ctx.Err() != nil {
		t.Fatalf("list capture failed: read=%v; deadline=%v", drainErr, ctx.Err())
	}
	if err := verifyFixture(waitErr, []byte(stderr.checkedText(t)), elapsed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("cancellation did not close the HTTP request")
	}
	if requests.Load() != 2 {
		t.Fatalf("interrupted list made %d API requests, want 2", requests.Load())
	}
	assertFiniteJSONListPartial(t, stdout.checkedText(t), "file_a")
	t.Log("finite-list CTRL_C verified: partial array preserved, HTTP source closed, two requests")
}
