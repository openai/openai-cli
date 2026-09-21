package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real main/command/error dispatch in a child process. A Unix PTY
// supplies terminal file descriptors without controlling a desktop terminal.
func TestMainImageErrorsFriendly(t *testing.T) {
	for _, test := range []struct {
		name, code, parameter, message string
		status                         int
		missingPrompt                  bool
	}{
		{name: "bad size", status: 400, parameter: "size", message: "The API rejected --size."},
		{name: "custom endpoint credentials", status: 401, code: "invalid_api_key", message: "credentials required by your custom API endpoint"},
		{name: "permissions", status: 403, message: "key permissions"},
		{name: "billing limit", status: 429, code: "insufficient_quota", message: "waiting alone may not fix this"},
		{name: "request rate", status: 429, code: "rate_limit_exceeded", message: "send fewer requests at once"},
		{name: "service failure", status: 500, message: "could not complete the request (HTTP 500)"},
		{name: "missing description", status: 400, missingPrompt: true, message: `./openai images generate --prompt "A tiny orange robot"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, count, _ := imageErrorTestServer(t, test.status, test.code, test.parameter)
			args := []string{"./openai", "--base-url", server.URL, "images", "generate", "--inline", "off"}
			wantRequests := int32(0)
			if !test.missingPrompt {
				args = append(args, "--prompt", "synthetic private description")
				wantRequests = 1
			}
			result := runMainImageErrorProcess(t, "terminal", args)
			if result.code != 1 || count.Load() != wantRequests {
				t.Fatalf("exit=%d requests=%d, want exit=1 requests=%d; %s", result.code, count.Load(), wantRequests, imageErrorTestOutput(result))
			}
			text := result.stdout + result.stderr
			if !strings.Contains(text, test.message) {
				t.Fatalf("missing friendly guidance %q: %s", test.message, imageErrorTestOutput(result))
			}
			wantNotices := 1
			if test.missingPrompt {
				wantNotices = 0
			}
			if got := strings.Count(text, "Generating image..."); got != wantNotices {
				t.Errorf("generation notice count = %d, want %d: %s", got, wantNotices, imageErrorTestOutput(result))
			}
			for _, hidden := range []string{"synthetic raw API detail", "synthetic private description", "synthetic-image-error-key", `"message":`, "POST \"", "\x1b"} {
				if strings.Contains(text, hidden) {
					t.Errorf("friendly output leaked %q: %s", hidden, imageErrorTestOutput(result))
				}
			}
		})
	}
}

func TestMainImageErrorsPreserveAPIOutput(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		flags      []string
		models     bool
	}{
		{name: "explicit JSON errors", mode: "terminal", flags: []string{"--format-error", "json"}},
		{name: "JSON errors override readable output", mode: "terminal", flags: []string{"--format", "text", "--format-error", "json"}},
		{name: "explicit JSON output", mode: "terminal", flags: []string{"--format", "json"}},
		{name: "redirected JSON output", mode: "redirect-stdout", flags: []string{"--format", "json"}},
		{name: "piped JSON output", mode: "pipes", flags: []string{"--format", "json"}},
		{name: "unrelated model JSON operation", mode: "terminal", flags: []string{"--format", "json"}, models: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, count, wantJSON := imageErrorTestServer(t, 400, "synthetic_bad_value", "size")
			args := append([]string{"./openai", "--base-url", server.URL}, test.flags...)
			if test.models {
				args = append(args, "models", "list")
			} else {
				args = append(args, "images", "generate", "--inline", "off", "--prompt", "synthetic private description")
			}
			result := runMainImageErrorProcess(t, test.mode, args)
			if result.code != 1 || count.Load() != 1 {
				t.Fatalf("exit=%d requests=%d, want one failed request; %s", result.code, count.Load(), imageErrorTestOutput(result))
			}
			if test.mode != "terminal" && result.stdout != "" {
				t.Errorf("error data was written to stdout: %q", result.stdout)
			}
			text := result.stdout + result.stderr
			start, end := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}')
			if start < 0 || end < start {
				t.Fatalf("original API JSON is missing: %s", imageErrorTestOutput(result))
			}
			var payload map[string]string
			if err := json.Unmarshal([]byte(text[start:end+1]), &payload); err != nil {
				t.Fatalf("API error is not valid JSON: %v; %s", err, imageErrorTestOutput(result))
			}
			if len(payload) != len(wantJSON) {
				t.Errorf("API error fields changed: got %v; want %v", payload, wantJSON)
			}
			for key, want := range wantJSON {
				if payload[key] != want {
					t.Errorf("API field %s=%q; want %q", key, payload[key], want)
				}
			}
			if strings.Contains(text, "Generating image") || strings.Contains(text, "The API rejected --size.") {
				t.Errorf("interactive progress leaked into API/script output: %s", imageErrorTestOutput(result))
			}
		})
	}
}

func TestMainImageErrorsReadableWhenRedirected(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		flags      []string
	}{
		{name: "piped default", mode: "pipes"},
		{name: "redirected default", mode: "redirect-stdout"},
		{name: "explicit auto errors", mode: "terminal", flags: []string{"--format-error", "auto"}},
		{name: "readable errors override JSON output", mode: "pipes", flags: []string{"--format", "json", "--format-error", "text"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, count, _ := imageErrorTestServer(t, 400, "synthetic_bad_value", "size")
			args := append([]string{"./openai", "--base-url", server.URL}, test.flags...)
			args = append(args, "images", "generate", "--inline", "off", "--prompt", "synthetic private description")
			result := runMainImageErrorProcess(t, test.mode, args)
			text := result.stdout + result.stderr
			if result.code != 1 || count.Load() != 1 || !strings.Contains(text, "--format-error json") {
				t.Fatalf("readable failure missing guidance: %s", imageErrorTestOutput(result))
			}
			if test.mode != "terminal" && result.stdout != "" {
				t.Errorf("error details were written to stdout: %q", result.stdout)
			}
			for _, hidden := range []string{"synthetic raw API detail", "synthetic private description", "synthetic-image-error-key", `"message":`, "\x1b"} {
				if strings.Contains(text, hidden) {
					t.Errorf("readable error exposed %q: %s", hidden, imageErrorTestOutput(result))
				}
			}
		})
	}
}

func imageErrorTestServer(t *testing.T, status int, code, parameter string) (*httptest.Server, *atomic.Int32, map[string]string) {
	t.Helper()
	payload := map[string]string{
		"message": "synthetic raw API detail", "type": "invalid_request_error", "code": code, "param": parameter,
	}
	count := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-should-retry", "false")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": payload})
	}))
	t.Cleanup(server.Close)
	return server, count, payload
}

func runMainImageErrorProcess(t *testing.T, mode string, argv []string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	stdoutPath := filepath.Join(dir, "stdout.txt")
	args := append([]string{"-test.run=^TestMainDispatchProcess$", "--"}, argv...)
	executable := binary
	if mode != "pipes" {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			t.Skip("PTY integration uses Unix script")
		}
		executable, err = exec.LookPath("script")
		if err != nil {
			t.Skip("script is unavailable for PTY integration")
		}
		child := append([]string{binary}, args...)
		if mode == "redirect-stdout" {
			// Positional shell arguments carry all CLI tokens unchanged; only our
			// quoted test path is redirected. No user command is interpolated.
			child = append([]string{"/bin/sh", "-c", `exec "$@" > "$OPENAI_CLI_TEST_STDOUT"`, "image-test"}, child...)
		}
		if runtime.GOOS == "darwin" {
			args = append([]string{"-q", "/dev/null"}, child...)
		} else {
			quoted := make([]string, len(child))
			for i, arg := range child {
				quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
			}
			args = []string{"-q", "-e", "-c", strings.Join(quoted, " "), "/dev/null"}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, args...)
	child.Dir = dir
	child.Stdin = strings.NewReader("")
	child.WaitDelay = time.Second
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "OPENAI_") {
			continue
		}
		switch name {
		case "CI", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "COMPLETION_STYLE", "FORCE_COLOR", "NO_COLOR", "CLICOLOR", "COLORTERM", "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TMUX", "STY", "ZELLIJ", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		child.Env = append(child.Env, entry)
	}
	child.Env = append(child.Env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_CLI_TEST_STDOUT="+stdoutPath,
		"OPENAI_API_KEY=synthetic-image-error-key", "HOME="+dir, "USERPROFILE="+dir,
		"XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir, "NO_PROXY=127.0.0.1,localhost",
		"FORCE_COLOR=0", "NO_COLOR=1", "CLICOLOR=0", "TERM=dumb", "TERM_PROGRAM=synthetic")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	if ctx.Err() != nil {
		t.Fatalf("image error child timed out: %v", ctx.Err())
	}
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if mode == "redirect-stdout" {
		redirected, err := os.ReadFile(stdoutPath)
		if err != nil {
			t.Fatal(err)
		}
		stderr.Write(stdout.Bytes()) // PTY stream contains the child's stderr.
		stdout.Reset()
		stdout.Write(redirected)
	}
	return mainDispatchResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func imageErrorTestOutput(result mainDispatchResult) string {
	text := result.stdout + result.stderr
	if len(text) > 3000 {
		text = text[:3000] + " [truncated]"
	}
	return fmt.Sprintf("output=%q", text)
}
