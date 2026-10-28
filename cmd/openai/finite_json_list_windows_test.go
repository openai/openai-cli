//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func setFiniteJSONListOutputProcess(child *exec.Cmd) {
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

func TestMainDispatchFiniteJSONListWindowsClosedConsumerMatchesPreservedOutput(t *testing.T) {
	// The preserved raw-output path buffers a screen before writing. Exceed its
	// 37-line fallback with one object so both paths can reach the same gate.
	fields := make(map[string]any, 64)
	for index := range 64 {
		fields[fmt.Sprintf("field_%02d", index)] = "synthetic"
	}
	first := map[string]any{"id": "file_a", "object": "file", "metadata": fields}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	firstPage := fmt.Sprintf(`{"object":"list","data":[%s],"has_more":true,"last_id":"file_a"}`, firstJSON)
	secondPage := fmt.Sprintf(`{"object":"list","data":[{"id":"file_b","object":"file","padding":%q}],"has_more":true,"last_id":"file_b"}`, strings.Repeat("x", 16384))
	const apiMessage = "synthetic later page failure"
	const localMessage = "A local file could not be read or written. Check your file arguments and permissions."
	type result struct {
		code       int
		diagnostic map[string]any
	}
	for _, upstreamFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("upstream_failure=%t", upstreamFailure), func(t *testing.T) {
			var results [2]result
			for index, rawOutput := range []bool{false, true} {
				if !t.Run(fmt.Sprintf("raw_output=%t", rawOutput), func(t *testing.T) {
					var requests atomic.Int32
					requested, release := make(chan struct{}), make(chan struct{})
					var releaseOnce sync.Once
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
							io.WriteString(w, firstPage)
							return
						}
						close(requested)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
						if upstreamFailure {
							w.WriteHeader(http.StatusBadRequest)
							io.WriteString(w, `{"error":{"message":"synthetic later page failure","type":"invalid_request_error"}}`)
							return
						}
						// More data remains available. A failed write must stop pagination.
						io.WriteString(w, secondPage)
					}))
					t.Cleanup(func() {
						releaseOnce.Do(func() { close(release) })
						server.CloseClientConnections()
						server.Close()
					})
					binary, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
					defer cancel()
					args := []string{"-test.run=^TestMainDispatchProcess$", "--", "openai", "--format", "json"}
					if rawOutput {
						args = append(args, "--raw-output")
					}
					child := exec.CommandContext(ctx, binary, append(args, "files", "list")...)
					home := t.TempDir()
					child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "HOME=" + home, "USERPROFILE=" + home,
						"OPENAI_API_KEY=sk-fake-list-test", "OPENAI_BASE_URL=" + server.URL,
						"NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb"}
					setFiniteJSONListOutputProcess(child)
					child.WaitDelay = 2 * time.Second
					reader, writer, err := os.Pipe()
					if err != nil {
						t.Fatal(err)
					}
					var stderr bytes.Buffer
					child.Stdout, child.Stderr = writer, &stderr
					started, reading := false, false
					waitDone, readDone := make(chan struct{}), make(chan struct{})
					var waitErr, readErr error
					var decoded map[string]any
					t.Cleanup(func() {
						cancel()
						writer.Close()
						reader.Close()
						if started {
							select {
							case <-waitDone:
							case <-time.After(4 * time.Second):
								t.Error("closed-consumer child did not join during cleanup")
							}
						}
						if reading {
							select {
							case <-readDone:
							case <-time.After(3 * time.Second):
								t.Error("first-object reader did not join during cleanup")
							}
						}
					})
					if err := child.Start(); err != nil {
						t.Fatal(err)
					}
					started = true
					go func() { waitErr = child.Wait(); close(waitDone) }()
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					reading = true
					go func() {
						defer close(readDone)
						decoder := json.NewDecoder(reader)
						if !rawOutput {
							var token json.Token
							token, readErr = decoder.Token()
							if readErr == nil && token != json.Delim('[') {
								readErr = fmt.Errorf("expected array prefix, got %v", token)
							}
						}
						if readErr == nil {
							readErr = decoder.Decode(&decoded)
						}
					}()
					select {
					case <-readDone:
					case <-ctx.Done():
						t.Fatal("first object did not arrive before the deadline")
					}
					if readErr != nil || !reflect.DeepEqual(decoded, first) {
						t.Fatalf("first object changed: value=%#v error=%v", decoded, readErr)
					}
					select {
					case <-requested:
					case <-waitDone:
						t.Fatalf("child exited before requesting page two: %v", waitErr)
					case <-ctx.Done():
						t.Fatal("page two was not requested before the deadline")
					}
					// No duplicated reader exists. Close the sole parent read handle
					// only after the complete first object and blocked second request.
					if err := reader.Close(); err != nil {
						t.Fatal(err)
					}
					releaseOnce.Do(func() { close(release) })
					select {
					case <-waitDone:
					case <-ctx.Done():
						t.Fatal("closed-consumer child did not exit before the deadline")
					}
					code := 0
					var exit *exec.ExitError
					if errors.As(waitErr, &exit) {
						code = exit.ExitCode()
					} else if waitErr != nil {
						t.Fatalf("child wait failed: %v", waitErr)
					}
					if ctx.Err() != nil || code < 0 || code > 1 {
						t.Fatalf("child did not exit normally: code=%d deadline=%v", code, ctx.Err())
					}
					if requests.Load() != 2 {
						t.Fatalf("closed consumer made %d requests, want exactly 2", requests.Load())
					}
					got := result{code: code}
					if code == 0 {
						if upstreamFailure || stderr.Len() != 0 {
							t.Fatalf("unexpected successful exit: upstream=%t stderr=%q", upstreamFailure, stderr.String())
						}
					} else {
						if err := json.Unmarshal(stderr.Bytes(), &got.diagnostic); err != nil {
							t.Fatalf("invalid complete diagnostic: %q error=%v", stderr.String(), err)
						}
						want := map[string]any{"message": localMessage}
						if upstreamFailure {
							want = map[string]any{"message": apiMessage, "type": "invalid_request_error"}
						}
						if !reflect.DeepEqual(got.diagnostic, want) {
							t.Fatalf("unexpected diagnostic: got=%#v want=%#v", got.diagnostic, want)
						}
					}
					results[index] = got
					t.Logf("Windows closed consumer: raw_output=%t upstream_failure=%t exit=%d requests=2", rawOutput, upstreamFailure, code)
				}) {
					return
				}
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatalf("finite JSON changed preserved output behavior: finite=%+v raw=%+v", results[0], results[1])
			}
		})
	}
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
	child, interrupt, verifyFixture := windowsInterruptCommand(t, ctx, binary, home, env, uint32(windows.STATUS_CONTROL_C_EXIT),
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

func TestFiniteJSONListInterruptControllerResult(t *testing.T) {
	controlExit := uint32(windows.STATUS_CONTROL_C_EXIT)
	for _, test := range []struct {
		name  string
		exit  int
		valid bool
	}{
		{"CTRL_C exit", int(controlExit), true},
		{"signed CTRL_C exit", int(int32(controlExit)), true},
		{"cooperative exit 130", 130, false},
		{"unrelated exit 1", 1, false},
		{"zero exit", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := windowsConsoleResult{Started: true, EventSent: true, Event: windows.CTRL_C_EVENT, ChildExit: &test.exit}
			if err := verifyWindowsInterruptControllerResult(result, test.exit, controlExit); (err == nil) != test.valid {
				t.Fatalf("valid=%v, want %v: %v", err == nil, test.valid, err)
			}
		})
	}
}
