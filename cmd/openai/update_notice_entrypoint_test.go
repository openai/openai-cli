package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/generated/commands"
)

const updateNoticeMessage = "Update to 99.0.0: https://github.com/openai/openai-cli#installation"

// Stdin and stdout share one terminal; stderr uses a separate terminal.
// Independent redirection detects admission and stderr buffer routing errors.
const updateNoticePTY = `
import errno, fcntl, json, os, pty, selectors, signal, struct, subprocess, sys, termios, time
redirect = sys.argv[1]
pairs = [pty.openpty() for _ in range(2)]
process = None
selector = selectors.DefaultSelector()
try:
    for master, slave in pairs:
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 100, 0, 0))
        assert os.isatty(slave)
    process = subprocess.Popen(
        sys.argv[2:],
        stdin=subprocess.DEVNULL if redirect == "stdin" else pairs[0][1],
        stdout=subprocess.PIPE if redirect == "stdout" else pairs[0][1],
        stderr=subprocess.PIPE if redirect == "stderr" else pairs[1][1],
        start_new_session=True,
    )
    for master, slave in pairs:
        os.close(slave)
    for index, stream in enumerate(("stdout", "stderr")):
        source = getattr(process, stream) if redirect == stream else pairs[index][0]
        selector.register(source, selectors.EVENT_READ, index)
    channels = [bytearray(), bytearray()]
    quit_sent = False
    deadline = time.monotonic() + 10
    while selector.get_map():
        if time.monotonic() >= deadline:
            raise TimeoutError("update notice entrypoint did not finish")
        for key, _ in selector.select(.1):
            try:
                data = os.read(key.fd, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if data:
                channels[key.data].extend(data)
                # Bare interactive help now waits for the user to close its viewer.
                if not quit_sent and key.data == 0 and b"Space: page   q: quit" in channels[0]:
                    os.write(pairs[0][0], b"q")
                    quit_sent = True
            else:
                selector.unregister(key.fileobj)
    code = process.wait(timeout=max(.1, deadline - time.monotonic()))
    print(json.dumps({"code": code, "stdout": channels[0].decode(), "stderr": channels[1].decode()}))
finally:
    if process is not None and process.poll() is None:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
    selector.close()
    if process is not None:
        for stream in (process.stdout, process.stderr):
            if stream is not None:
                stream.close()
    for pair in pairs:
        for descriptor in pair:
            try:
                os.close(descriptor)
            except OSError as error:
                if error.errno != errno.EBADF:
                    raise
`

type updateNoticeFixture struct {
	python, binary, home, state string
	env                         map[string]string
}

func newUpdateNoticeFixture(t *testing.T) updateNoticeFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Python POSIX PTYs do not provide native Windows terminal coverage")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("update notice entrypoint tests require python3 for POSIX PTYs")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	if runtime.GOOS == "darwin" {
		cache = filepath.Join(home, "Library", "Caches")
	}
	directory := filepath.Join(cache, "openai-cli", "update-notice")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(directory, "state.json")
	// Fresh synthetic metadata avoids network requests, even if a gate regresses.
	// A rejected invocation must not create the checker's .lock or rewrite state.
	data := fmt.Sprintf(`{"schema":1,"checked":%d,"notified":0,"latest":"99.0.0"}`+"\n", time.Now().Add(-time.Minute).Unix())
	if err := os.WriteFile(state, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return updateNoticeFixture{python, binary, home, state, map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CACHE_HOME": cache, "LOCALAPPDATA": cache,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "APPDATA": filepath.Join(home, "config"),
		"PATH": filepath.Join(home, "synthetic-bin"), "GOENV": "off",
		"TERM": "xterm-256color", "NO_COLOR": "1", "FORCE_COLOR": "0", "GOMAXPROCS": "2",
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1", "COMPLETION_STYLE": "bash",
		"OPENAI_BASE_URL": "http://127.0.0.1:1",
	}}
}

func (fixture updateNoticeFixture) run(t *testing.T, redirect string, argv ...string) mainDispatchResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"-I", "-B", "-c", updateNoticePTY, redirect, fixture.binary, "-test.run=^TestMainDispatchProcess$", "--", "openai"}
	args = append(args, argv...)
	child := exec.CommandContext(ctx, fixture.python, args...)
	// Let Python run its finally block and reap the owned CLI process group.
	child.Cancel = func() error { return child.Process.Signal(os.Interrupt) }
	child.WaitDelay = 2 * time.Second
	// An explicit environment excludes personal credentials and shell settings.
	for name, value := range fixture.env {
		child.Env = append(child.Env, name+"="+value)
	}
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("PTY helper failed: %v; output=%s", err, output)
	}
	var result struct {
		Code           int
		Stdout, Stderr string
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid PTY result: %v; output=%s", err, output)
	}
	return mainDispatchResult{result.Code, result.Stdout, result.Stderr}
}

type updateNoticeFile struct {
	Mode     fs.FileMode
	Modified int64
	Content  string
}

func updateNoticeSnapshot(t *testing.T, home string) map[string]updateNoticeFile {
	t.Helper()
	files := map[string]updateNoticeFile{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file := updateNoticeFile{Mode: info.Mode(), Modified: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file.Content = string(data)
		} else if !info.IsDir() {
			return fmt.Errorf("unexpected fixture file type: %s", info.Mode())
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		files[relative] = file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertUpdateNoticeUnchanged(t *testing.T, fixture updateNoticeFixture, before map[string]updateNoticeFile) {
	t.Helper()
	if after := updateNoticeSnapshot(t, fixture.home); !reflect.DeepEqual(before, after) {
		t.Fatalf("invocation changed isolated state: before=%v after=%v", before, after)
	}
}

func TestMainUpdateNoticeTerminalAndRepeat(t *testing.T) {
	fixture := newUpdateNoticeFixture(t)
	first := fixture.run(t, "")
	if first.code != 0 || first.stdout == "" || strings.Contains(first.stdout, "Update to ") || strings.TrimSpace(first.stderr) != updateNoticeMessage {
		t.Fatalf("notice did not reach actual stderr: %+v", first)
	}
	data, err := os.ReadFile(fixture.state)
	if err != nil {
		t.Fatal(err)
	}
	var state struct{ Notified int64 }
	if err := json.Unmarshal(data, &state); err != nil || state.Notified == 0 {
		t.Fatalf("first notice did not persist its throttle: %s; error=%v", data, err)
	}
	before := updateNoticeSnapshot(t, fixture.home)
	second := fixture.run(t, "")
	if second.code != 0 || second.stdout != first.stdout || second.stderr != "" {
		t.Fatalf("repeated invocation changed help or repeated the notice: %+v", second)
	}
	assertUpdateNoticeUnchanged(t, fixture, before)
}

func TestMainUpdateNoticeCurrentVersionStaysSilent(t *testing.T) {
	for _, latest := range []string{commands.Version, "0.0.0"} {
		t.Run(latest, func(t *testing.T) {
			fixture := newUpdateNoticeFixture(t)
			data, err := os.ReadFile(fixture.state)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), `"99.0.0"`, fmt.Sprintf("%q", latest), 1))
			if err := os.WriteFile(fixture.state, data, 0600); err != nil {
				t.Fatal(err)
			}
			got := fixture.run(t, "")
			if got.code != 0 || got.stdout == "" || got.stderr != "" || strings.Contains(got.stdout, "Update to ") {
				t.Fatalf("current/newer installation displayed a notice: %+v", got)
			}
			after, err := os.ReadFile(fixture.state)
			if err != nil || string(after) != string(data) {
				t.Fatalf("silent invocation consumed the notice interval: state=%s; error=%v", after, err)
			}
		})
	}
}

func TestMainUpdateNoticeRedirectedStreams(t *testing.T) {
	for _, redirect := range []string{"stdin", "stdout", "stderr"} {
		t.Run(redirect, func(t *testing.T) {
			fixture := newUpdateNoticeFixture(t)
			before := updateNoticeSnapshot(t, fixture.home)
			got := fixture.run(t, redirect)
			if got.code != 0 || got.stdout == "" || got.stderr != "" || strings.Contains(got.stdout, "Update to ") {
				t.Fatalf("redirected invocation changed output: %+v", got)
			}
			assertUpdateNoticeUnchanged(t, fixture, before)
		})
	}
}

func TestMainUpdateNoticeEnvironmentExclusions(t *testing.T) {
	for _, pair := range [][2]string{
		{"CI", "1"}, {"GITHUB_ACTIONS", "true"}, {"GITLAB_CI", "1"},
		{"TF_BUILD", "True"}, {"BUILDKITE", "true"}, {"JENKINS_URL", "synthetic"},
		{"TEAMCITY_VERSION", "synthetic"}, {"TERM", "dumb"}, {"OPENAI_CLI_NO_UPDATE_CHECK", "1"},
	} {
		t.Run(pair[0], func(t *testing.T) {
			fixture := newUpdateNoticeFixture(t)
			fixture.env[pair[0]] = pair[1]
			before := updateNoticeSnapshot(t, fixture.home)
			got := fixture.run(t, "")
			if got.code != 0 || got.stdout == "" || got.stderr != "" || strings.Contains(got.stdout, "Update to ") {
				t.Fatalf("excluded environment changed output: %+v", got)
			}
			assertUpdateNoticeUnchanged(t, fixture, before)
		})
	}
}

func TestMainUpdateNoticeExplicitArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"--version"}, {"--quiet"}, {"--quiet=false"},
		{"__complete", "--", "mo"}, {"models"}, {"models", "list", "--help"},
		{"--format", "json"}, {"--format", "jsonl", "--help"},
		{"--format-error", "json", "--help"}, {"--raw-output", "--help"},
		{"--transform", "id", "--help"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			fixture := newUpdateNoticeFixture(t)
			before := updateNoticeSnapshot(t, fixture.home)
			got := fixture.run(t, "", args...)
			if got.code != 0 || got.stdout == "" || got.stderr != "" || strings.Contains(got.stdout, "Update to ") {
				t.Fatalf("explicit invocation changed output: %+v", got)
			}
			assertUpdateNoticeUnchanged(t, fixture, before)
		})
	}
}

func TestMainUpdateNoticeFailuresPreserveExitStatus(t *testing.T) {
	t.Run("invalid_flag", func(t *testing.T) {
		fixture := newUpdateNoticeFixture(t)
		before := updateNoticeSnapshot(t, fixture.home)
		got := fixture.run(t, "", "--synthetic-unknown-flag")
		if got.code != 1 || got.stderr == "" || strings.Contains(got.stdout+got.stderr, "Update to ") {
			t.Fatalf("parse failure changed: %+v", got)
		}
		assertUpdateNoticeUnchanged(t, fixture, before)
	})
	t.Run("api_error_json", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-should-retry", "false")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"error":{"message":"synthetic denial","code":"synthetic_denial"}}`)
		}))
		defer server.Close()
		fixture := newUpdateNoticeFixture(t)
		fixture.env["OPENAI_BASE_URL"] = server.URL
		fixture.env["OPENAI_API_KEY"] = "synthetic-update-notice-key"
		before := updateNoticeSnapshot(t, fixture.home)
		got := fixture.run(t, "", "--format-error", "json", "models", "retrieve", "--model", "synthetic-model")
		if got.code != 1 || got.stdout != "" || requests.Load() != 1 {
			t.Fatalf("API failure changed: %+v requests=%d", got, requests.Load())
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(got.stderr), &payload); err != nil || payload["code"] != "synthetic_denial" {
			t.Fatalf("notice polluted structured error: %q; error=%v", got.stderr, err)
		}
		assertUpdateNoticeUnchanged(t, fixture, before)
	})
}
