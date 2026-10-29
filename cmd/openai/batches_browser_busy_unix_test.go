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
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Exercise the production input interceptor. Sending a tea.KeyPressMsg directly
// to the busy model bypasses the layer that owns keyboard cancellation.
func TestMainBatchBrowserBusyKeyboardCancellation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for the Unix Batch browser PTY cases")
	}
	harness, err := filepath.Abs("../../scripts/image_picker_harness.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"list", "upload", "create"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			work := t.TempDir()
			input := []byte(`{"custom_id":"synthetic","method":"POST","url":"/v1/responses","body":{"model":"synthetic","input":"hello"}}` + "\n")
			if err := os.WriteFile(filepath.Join(work, "requests.jsonl"), input, 0600); err != nil {
				t.Fatal(err)
			}
			var lists, uploads, creates atomic.Int32
			interrupted := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /batches":
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
					return
				case "GET /files":
					lists.Add(1)
				case "POST /files":
					uploads.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					if phase == "create" {
						_, _ = io.WriteString(w, `{"object":"file","id":"file_synthetic","filename":"requests.jsonl","purpose":"batch","bytes":123}`)
						return
					}
				case "POST /batches":
					creates.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
				default:
					t.Errorf("unexpected synthetic request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if err := os.WriteFile(filepath.Join(work, "entered"), nil, 0600); err != nil {
					t.Error(err)
					return
				}
				// No response can race the literal Ctrl+C from the PTY.
				select {
				case <-r.Context().Done():
					select {
					case interrupted <- struct{}{}:
					default:
					}
				case <-ctx.Done():
				}
			}))
			defer func() { cancel(); server.Close() }()
			child := batchesWorkflowProcess(t, ctx, server, "batches", "browse")
			child.Env = append(child.Env, "HOME="+work, "TMPDIR="+work,
				"XDG_CONFIG_HOME="+work, "XDG_CACHE_HOME="+work, "TERM=xterm-256color",
				"NO_COLOR=1", "OPENAI_CLI_NO_UPDATE_CHECK=1")
			config, err := json.Marshal(map[string]any{
				"binary": child.Path, "args": child.Args[1:], "env": child.Env,
				"harness": harness, "work": work, "phase": phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			probe := exec.CommandContext(ctx, python, "-I", "-S", "-B", "-c", batchesBrowserBusyPTY)
			probe.Stdin = bytes.NewReader(config)
			probe.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work}
			probe.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			probe.Cancel = func() error { return probe.Process.Signal(syscall.SIGTERM) }
			probe.WaitDelay = 4 * time.Second
			var output adminSetupTerminalCapture
			probe.Stdout, probe.Stderr = &output, &output
			err = probe.Run()
			if probe.Process != nil && syscall.Kill(-probe.Process.Pid, 0) != syscall.ESRCH {
				_ = syscall.Kill(-probe.Process.Pid, syscall.SIGKILL)
				t.Error("Batch browser PTY required forced process-group cleanup")
			}
			if err != nil || output.overflow || ctx.Err() != nil {
				t.Fatalf("busy %s cancellation failed: %v; context=%v; output=%s", phase, err, ctx.Err(), output.buffer.String())
			}
			select {
			case <-interrupted:
			case <-time.After(time.Second):
				t.Fatal("Ctrl+C did not cancel the active HTTP request")
			}
			wantLists, wantUploads, wantCreates := int32(0), int32(0), int32(0)
			if phase == "list" {
				wantLists = 1
			} else {
				wantUploads = 1
				if phase == "create" {
					wantCreates = 1
				}
			}
			if lists.Load() != wantLists || uploads.Load() != wantUploads || creates.Load() != wantCreates {
				t.Fatalf("unexpected request counts: list=%d upload=%d create=%d", lists.Load(), uploads.Load(), creates.Load())
			}
			entries, err := os.ReadDir(work)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "requests.jsonl" && entry.Name() != "entered" {
					t.Errorf("browser left an owned scratch file: %s", entry.Name())
				}
			}
		})
	}
}

const batchesBrowserBusyPTY = `
import importlib.util, json, os, signal, subprocess, sys, time
from pathlib import Path

config = json.load(sys.stdin)
spec = importlib.util.spec_from_file_location('picker_harness', config['harness'])
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)

def stop(signum, frame):
    raise SystemExit(128 + signum)

signal.signal(signal.SIGTERM, stop)
original_popen = subprocess.Popen
children = []

def owned_popen(*args, **kwargs):
    # Keep the CLI inside the fixture's process group for the Go timeout fallback.
    kwargs['start_new_session'] = False
    child = original_popen(*args, **kwargs)
    children.append(child)
    return child

terminal = None
try:
    subprocess.Popen = owned_popen
    terminal = harness.Terminal(config['binary'], config['args'],
        dict(entry.split('=', 1) for entry in config['env']), width=100, height=32,
        cwd=config['work'], deadline=time.monotonic()+16,
        max_output_bytes=2 << 20, max_events=2000, max_event_bytes=2 << 20)
    subprocess.Popen = original_popen

    def view():
        return harness.screen(terminal.raw, 100, 32).decode('utf-8', 'replace')

    def wait(predicate):
        until = time.monotonic()+4
        while not predicate(view()):
            terminal.read(0.03)
            if terminal.child.poll() is not None or time.monotonic() > until:
                raise AssertionError('missing expected browser state: '+view())

    def shown(*labels):
        wait(lambda text: all(label in text for label in labels))

    shown('Search', 'Ctrl+N')
    terminal.send(b'\x0e')
    shown('Create batch', 'Input file')
    terminal.send(b'\r')
    shown('Choose input file', 'Choose an uploaded file', 'requests.jsonl')
    if config['phase'] == 'list':
        terminal.send(b'uploaded\r')
    else:
        terminal.send(b'requests.jsonl\r')
        shown('Review batch', 'requests.jsonl', 'Enter submits one batch')
        if (Path(config['work'])/'entered').exists():
            raise AssertionError('the browser started a request before confirmation')
        # Test Enter and the retained Ctrl+S shortcut against active HTTP requests.
        terminal.send(b'\r' if config['phase'] == 'upload' else b'\x13')
    wait(lambda text: (Path(config['work'])/'entered').exists() and 'Ctrl+C' in text)
    shown('Loading uploaded input files' if config['phase'] == 'list' else 'Creating batch')
    terminal.send(b'\x03')
    terminal.finish(130, timeout=3, expect_alternate_screen=True)
finally:
    subprocess.Popen = original_popen
    for child in children:
        if child.poll() is None:
            child.kill()
        child.wait(timeout=3)
    if terminal is not None:
        terminal.close()
`
