//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCaptureRecorderExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name             string
		actual, expected int
		wantSuccess      bool
	}{
		{"success", 0, 0, true},
		{"expected command failure", 1, 1, true},
		{"unexpected recorder failure", 31, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := newCaptureTest(t)
			cmd, output := capture.command(t, captureTestScene+captureTestFinish,
				"DEMO_TEST_RECORDER_STATUS="+strconv.Itoa(tc.actual),
				"DEMO_TEST_EXPECTED_STATUS="+strconv.Itoa(tc.expected))
			err := cmd.Run()
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("unexpected recorder result: %v\n%s", err, output)
			}
			capture.requireFile(t, "completed", tc.wantSuccess)
			_, pngErr := os.Stat(filepath.Join(capture.media, "before.png"))
			if (pngErr == nil) != tc.wantSuccess {
				t.Fatalf("unexpected rendered screenshot state: %v", pngErr)
			}
			metadata, err := os.ReadFile(filepath.Join(capture.media, "metadata.txt"))
			if err != nil || !strings.Contains(string(metadata), "exit status: "+strconv.Itoa(tc.actual)) {
				t.Fatalf("recorder status not retained: %v\n%s", err, metadata)
			}
			capture.requireCleanup(t)
		})
	}
}

func TestCaptureRenderFailureStopsRecording(t *testing.T) {
	capture := newCaptureTest(t)
	cmd, output := capture.command(t, captureTestScene+captureTestFinish, "DEMO_TEST_RENDER_STATUS=23")
	requireCaptureExit(t, cmd.Run(), 23, output)
	capture.requireFile(t, "completed", false)
	if _, err := os.Stat(filepath.Join(capture.media, "before.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("render failure still produced a screenshot: %v", err)
	}
	capture.requireCleanup(t)
}

func TestCaptureFixtureShutdownFailureIsRetained(t *testing.T) {
	capture := newCaptureTest(t)
	cmd, output := capture.command(t, captureTestScene+captureTestFinish, "DEMO_TEST_SHUTDOWN_STATUS=37")
	requireCaptureExit(t, cmd.Run(), 1, output)
	if !strings.Contains(output.String(), "failed during shutdown") {
		t.Fatalf("missing fixture shutdown diagnostic: %s", output)
	}
	capture.requireFile(t, "completed", false)
	capture.requireCleanup(t)
}

func TestCaptureSignalsCleanUpFixtureAndTemporaryFiles(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			capture := newCaptureTest(t)
			cmd, output := capture.command(t, `demo_capture_scene before 0 "$demo_runtime/before" "$demo_api_url" "Synthetic test" "DEMO_TEST_BLOCK_CAPTURE=1" "DEMO_TEST_STATE=$DEMO_TEST_STATE"
`+captureTestFinish)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(capture.state, "capture.ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					_ = cmd.Wait()
					t.Fatalf("recorder did not become ready: %s", output)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := cmd.Process.Signal(signal); err != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				_ = cmd.Wait()
				t.Fatal(err)
			}
			requireCaptureExit(t, cmd.Wait(), 128+int(signal), output)
			capture.requireFile(t, "completed", false)
			capture.requireStoppedProcess(t, "capture")
			capture.requireCleanup(t)
		})
	}
}

func TestCapturePreservesExistingRecordings(t *testing.T) {
	capture := newCaptureTest(t)
	previous := filepath.Join(capture.media, "before.cast")
	if err := os.WriteFile(previous, []byte("previous reviewed recording\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, output := capture.command(t, captureTestScene+captureTestFinish)
	requireCaptureExit(t, cmd.Run(), 2, output)
	contents, err := os.ReadFile(previous)
	if err != nil || string(contents) != "previous reviewed recording\n" {
		t.Fatalf("existing recording changed: %v\n%s", err, contents)
	}
	capture.requireFile(t, "fixture.pid", false)
	entries, err := os.ReadDir(capture.temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected output allocated temporary state: %v, %v", entries, err)
	}
}

func TestCaptureRejectsUnavailableBinary(t *testing.T) {
	for _, missing := range []bool{true, false} {
		name := "nonexecutable"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			capture := newCaptureTest(t)
			binary := filepath.Join(capture.tools, "openai")
			var err error
			if missing {
				err = os.Remove(binary)
			} else {
				err = os.Chmod(binary, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			cmd, output := capture.command(t, captureTestScene+captureTestFinish)
			requireCaptureExit(t, cmd.Run(), 1, output)
			capture.requireFile(t, "fixture.pid", false)
			capture.requireFile(t, "runtime", false)
		})
	}
}

const captureTestScene = `demo_capture_scene before "${DEMO_TEST_EXPECTED_STATUS:-0}" "$demo_runtime/before" "$demo_api_url" "Synthetic test" "DEMO_TEST_RECORDER_STATUS=${DEMO_TEST_RECORDER_STATUS:-0}"
`

const captureTestFinish = `demo_stop_api
printf completed > "$DEMO_TEST_STATE/completed"
`

type captureTest struct {
	root, state, media, temporary, tools, helper string
}

func newCaptureTest(t *testing.T) *captureTest {
	t.Helper()
	directory := t.TempDir()
	helper, err := filepath.Abs("capture_and_render.sh")
	if err != nil {
		t.Fatal(err)
	}
	capture := &captureTest{
		root: filepath.Join(directory, "repository"), state: filepath.Join(directory, "state"),
		media: filepath.Join(directory, "media"), temporary: filepath.Join(directory, "temporary"),
		tools: filepath.Join(directory, "tools"), helper: helper,
	}
	for _, dir := range []string{capture.root, capture.state, capture.media, capture.temporary, capture.tools} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"openai": "exit 0\n",
		"asciinema": `case "$1" in
  rec)
    if [ "${DEMO_TEST_BLOCK_CAPTURE:-0}" -eq 1 ]; then
      printf '%s\n' "$$" > "$DEMO_TEST_STATE/capture.pid"
      trap 'printf stopped > "$DEMO_TEST_STATE/capture.stopped"; exit 0' INT TERM
      printf ready > "$DEMO_TEST_STATE/capture.ready"
      while :; do sleep 0.02; done
    fi
    printf synthetic > "${!#}"
    exit "${DEMO_TEST_RECORDER_STATUS:-0}";;
  convert) printf synthetic > "${!#}";;
esac
`,
		"agg": `if [ "${DEMO_TEST_RENDER_STATUS:-0}" -ne 0 ]; then exit "$DEMO_TEST_RENDER_STATUS"; fi
printf synthetic > "${!#}"
`,
		"ffmpeg":  "printf synthetic > \"${!#}\"\n",
		"ffprobe": "printf '{}\\n'\n",
		"fixture": `printf '%s\n' "$$" > "$DEMO_TEST_STATE/fixture.pid"
trap 'printf stopped > "$DEMO_TEST_STATE/fixture.stopped"; exit "${DEMO_TEST_SHUTDOWN_STATUS:-0}"' INT TERM
printf 'http://127.0.0.1:1/v1\n' > "$1"
while :; do sleep 0.02; done
`,
	} {
		if err := os.WriteFile(filepath.Join(capture.tools, name), []byte("#!/bin/bash\nset -eu\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return capture
}

func (c *captureTest) command(t *testing.T, body string, settings ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	script := `set -euo pipefail
source "$1"
demo_window_size=90x24
demo_render_options=(--font-size 22)
demo_prepare_capture "$2" "$3" "$3" "$4" "$4" "$5" "$6"
printf '%s\n' "$demo_runtime" > "$DEMO_TEST_STATE/runtime"
printf '#!/bin/bash\nexit 0\n' > "$demo_runtime/scene.sh"
demo_start_api
` + body
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", script, "capture-test",
		c.helper, c.root, filepath.Join(c.tools, "openai"), strings.Repeat("a", 40), c.media, filepath.Join(c.tools, "fixture"))
	cmd.Env = append([]string{"PATH=" + c.tools + ":/usr/bin:/bin", "TMPDIR=" + c.temporary, "DEMO_TEST_STATE=" + c.state}, settings...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	// Do not leak a fixture or recorder if the cleanup behavior under test regresses.
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	output := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = output, output
	return cmd, output
}

func (c *captureTest) requireFile(t *testing.T, name string, exists bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(c.state, name))
	if exists && err != nil || !exists && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected %s state: %v", name, err)
	}
}

func (c *captureTest) requireCleanup(t *testing.T) {
	t.Helper()
	runtime, err := os.ReadFile(filepath.Join(c.state, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(runtime))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary capture directory remains: %v", err)
	}
	c.requireStoppedProcess(t, "fixture")
}

func (c *captureTest) requireStoppedProcess(t *testing.T, name string) {
	t.Helper()
	c.requireFile(t, name+".stopped", true)
	pidBytes, err := os.ReadFile(filepath.Join(c.state, name+".pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("%s process %d still exists: %v", name, pid, err)
	}
}

func requireCaptureExit(t *testing.T, err error, want int, output *bytes.Buffer) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != want {
		t.Fatalf("want exit %d, got %v\n%s", want, err, output)
	}
}
