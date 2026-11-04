//go:build darwin || linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/app"
)

func TestMainAutomaticUpdateEligibilityAndConfirmation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("automatic updates require an ordinary user")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("production-entrypoint test requires POSIX Python PTYs")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, ".openai", "bin")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "openai")
	const commit = "1111111111111111111111111111111111111111"
	flags := "-X github.com/openai/openai-cli/internal/updatenotice.releaseVersion=" + app.Version + " -X github.com/openai/openai-cli/internal/updatenotice.releaseCommit=" + commit
	build := exec.Command("go", "build", "-ldflags", flags, "-o", binary, "main.go")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real entrypoint: %v: %s", err, output)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binaryBytes)
	toolsDirectory := filepath.Join(home, "tools")
	if err := os.Mkdir(toolsDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDirectory, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(directory, ".update")
	if err := os.Mkdir(stateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDirectory, "state.json")
	state := map[string]any{"schema": 1, "checked": time.Now().Unix(), "activation_checked": 0, "applied": app.Version, "announced": false, "pending": map[string]any{
		"release":     map[string]any{"version": app.Version, "commit": commit, "release_id": 1, "asset_id": 2, "digest": strings.Repeat("a", 64), "size": 10, "updated": "2026-10-22T00:00:00Z"},
		"binary_hash": hex.EncodeToString(digest[:]), "previous_hash": strings.Repeat("b", 64),
	}}
	writeState := func() []byte {
		t.Helper()
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, data, 0600); err != nil {
			t.Fatal(err)
		}
		return data
	}
	environment := []string{"HOME=" + home, "PATH=" + toolsDirectory, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "TERM=xterm-256color", "NO_COLOR=1", "FORCE_COLOR=0", "GOMAXPROCS=2", "HTTPS_PROXY=http://127.0.0.1:1", "NO_PROXY=", "OPENAI_BASE_URL=http://127.0.0.1:1"}
	run := func(redirect string, extra []string, args ...string) mainDispatchResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		arguments := []string{"-I", "-B", "-c", updateNoticePTY, redirect, binary}
		arguments = append(arguments, args...)
		command := exec.CommandContext(ctx, python, arguments...)
		command.Env = append(append([]string{}, environment...), extra...)
		command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
		command.WaitDelay = 2 * time.Second
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("real CLI PTY failed: %v: %s", err, output)
		}
		var result struct {
			Code           int
			Stdout, Stderr string
		}
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatal(err)
		}
		return mainDispatchResult{result.Code, result.Stdout, result.Stderr}
	}
	message := "Updated to v" + app.Version + "."
	noticeDirectory := filepath.Join(home, "cache", "openai-cli", "update-notice-archive-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "darwin" {
		noticeDirectory = filepath.Join(home, "Library", "Caches", "openai-cli", "update-notice-archive-"+runtime.GOOS+"-"+runtime.GOARCH)
	}
	if err := os.MkdirAll(noticeDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	writeNotice := func() {
		t.Helper()
		data, err := json.Marshal(map[string]any{"schema": 1, "checked": time.Now().Unix(), "notified": 0, "latest": "99.0.0"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(noticeDirectory, "state.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, redirect string
		extra, args    []string
	}{
		{"explicit_help", "", nil, []string{"--help"}},
		{"help_command", "", nil, []string{"help"}},
		{"version", "", nil, []string{"--version"}},
		{"completion", "", nil, []string{"@completion", "bash"}},
		{"quiet", "", nil, []string{"--quiet"}},
		{"structured", "", nil, []string{"--format", "json"}},
		{"command_failure", "", nil, []string{"not-a-command"}},
		{"stdin", "stdin", nil, nil}, {"stdout", "stdout", nil, nil}, {"stderr", "stderr", nil, nil},
		{"ci", "", []string{"CI=1"}, nil}, {"dumb", "", []string{"TERM=dumb"}, nil},
		{"existing_optout", "", []string{"OPENAI_CLI_NO_UPDATE_CHECK=1"}, nil},
		{"auto_optout", "", []string{"OPENAI_CLI_NO_AUTO_UPDATE=1"}, nil},
		{"both_optouts", "", []string{"OPENAI_CLI_NO_AUTO_UPDATE=1", "OPENAI_CLI_NO_UPDATE_CHECK=1"}, nil},
		{"auto_optout_pin", "", []string{"OPENAI_CLI_NO_AUTO_UPDATE=1", "OPENAI_CLI_VERSION_PIN=v0.0.1"}, nil},
		{"conflicting_pin", "", []string{"OPENAI_CLI_VERSION_PIN=v0.0.1"}, nil},
		{"invalid_pin", "", []string{"OPENAI_CLI_VERSION_PIN=latest"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := writeState()
			writeNotice()
			result := run(test.redirect, test.extra, test.args...)
			if strings.Contains(result.stdout+result.stderr, message) {
				t.Fatal("ineligible invocation consumed confirmation")
			}
			after, err := os.ReadFile(statePath)
			if err != nil || string(after) != string(before) {
				t.Fatalf("ineligible invocation changed updater state: %v", err)
			}
			if test.name == "command_failure" && result.code == 0 {
				t.Fatal("updater suppressed error exit")
			}
			if test.name == "auto_optout" {
				if result.code != 0 || strings.TrimSpace(result.stderr) != updateNoticeMessage {
					t.Fatalf("automatic-install opt-out suppressed manual notice: %+v", result)
				}
			} else if strings.Contains(result.stdout+result.stderr, "Update to 99.0.0") {
				t.Fatal("ineligible invocation emitted a manual notice")
			}
		})
	}
	t.Run("confirmation_once", func(t *testing.T) {
		writeState()
		first := run("", nil)
		second := run("", nil)
		if first.code != 0 || second.code != 0 || !sameUpdatePTYOutput(first.stdout, second.stdout) {
			t.Fatal("updater changed help or exit status")
		}
		if strings.TrimSpace(first.stderr) != message || strings.Contains(second.stderr, "Updated to") {
			t.Fatalf("confirmation not delivered once: first=%q second=%q", first.stderr, second.stderr)
		}
		if !strings.Contains(first.stdout, "openai") {
			t.Fatal("confirmation appeared without help")
		}
	})
	waitForWorker := func(t *testing.T, key string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			data, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			var state map[string]any
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if timestamp, _ := state[key].(float64); timestamp > 0 {
				lock, err := os.Open(filepath.Join(stateDirectory, ".lock"))
				if err != nil {
					t.Fatal(err)
				}
				err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
				lock.Close()
				if err == nil {
					return state
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("worker did not finish %s: %s", key, data)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Run("ordinary_API_response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/models/test-model":
				fmt.Fprint(w, `{"object":"model","id":"test-model","created":1,"owned_by":"test-owner"}`)
			case "/v1/responses":
				fmt.Fprint(w, `{"id":"resp-test","object":"response","status":"completed","output":[]}`)
			case "/v1/files/file-test/content":
				w.Header().Set("Content-Type", "application/octet-stream")
				fmt.Fprint(w, "test download")
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()
		env := []string{"OPENAI_BASE_URL=" + server.URL + "/v1", "OPENAI_API_KEY=sk-test-only"}
		args := []string{"models", "retrieve", "test-model"}
		state["checked"] = 0 // A legacy receipt is due for another discovery.
		for _, tc := range []struct {
			name, redirect string
			prefix, extra  []string
			operands       []string
		}{
			{name: "quiet", prefix: []string{"--quiet"}},
			{name: "format", prefix: []string{"--format", "json"}},
			{name: "extract", prefix: []string{"--transform", "id"}},
			{name: "raw", prefix: []string{"--raw-output"}},
			{name: "explicit_help", operands: []string{"models", "retrieve", "--help"}},
			{name: "save", operands: []string{"files", "download", "file-test", "--output", filepath.Join(t.TempDir(), "result")}},
			{name: "failed_request", operands: []string{"models", "retrieve", "missing"}},
			{name: "stdin", redirect: "stdin"}, {name: "stdout", redirect: "stdout"}, {name: "stderr", redirect: "stderr"},
			{name: "ci", extra: []string{"CI=true"}}, {name: "dumb", extra: []string{"TERM=dumb"}},
			{name: "no_update_check", extra: []string{"OPENAI_CLI_NO_UPDATE_CHECK=1"}},
			{name: "no_auto_update", extra: []string{"OPENAI_CLI_NO_AUTO_UPDATE=1"}},
			{name: "unmanaged", extra: []string{"HOME=" + t.TempDir()}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := writeState()
				writeNotice()
				operands := tc.operands
				if operands == nil {
					operands = args
				}
				got := run(tc.redirect, append(append([]string{}, env...), tc.extra...), append(tc.prefix, operands...)...)
				if (got.code != 0) != (tc.name == "failed_request") ||
					strings.Contains(got.stderr, "Update to ") || strings.Contains(got.stderr, "Updated to ") {
					t.Fatalf("unexpected command result: %+v", got)
				}
				after, err := os.ReadFile(statePath)
				if err != nil || string(after) != string(before) {
					t.Fatalf("ineligible command touched update state: %v, %s", err, after)
				}
			})
		}
		baseline := run("", append(env, "OPENAI_CLI_NO_UPDATE_CHECK=1"), args...)
		if baseline.code != 0 || !strings.Contains(baseline.stdout, "test-model") {
			t.Fatalf("synthetic model request failed: %+v", baseline)
		}
		runOrdinary := func(args []string, expected mainDispatchResult) {
			t.Helper()
			got := run("", env, args...)
			if got != expected {
				t.Fatalf("ordinary command output changed: %+v", got)
			}
		}
		request := []string{"responses", "create", "--model", "test-model", "--input", "test input"}
		response := run("", append(env, "OPENAI_CLI_NO_UPDATE_CHECK=1"), request...)
		if response.code != 0 {
			t.Fatalf("synthetic response request failed: %+v", response)
		}
		writeState()
		runOrdinary(request, response)
		waitForWorker(t, "checked")
		runOrdinary(args, baseline)
		confirmed := run("", nil)
		if confirmed.code != 0 || strings.TrimSpace(confirmed.stderr) != message {
			t.Fatalf("silent discovery consumed the bare-help confirmation: %+v", confirmed)
		}
		// Seed a staged release. Revalidation uses the unreachable loopback proxy.
		// The command must schedule activation even when discovery is throttled.
		pending := state["pending"].(map[string]any)
		pending["release"].(map[string]any)["version"] = "99.0.0"
		state["checked"], state["applied"] = time.Now().Unix(), ""
		writeState()
		runOrdinary(args, baseline)
		waitForWorker(t, "activation_checked")
	})
	t.Run("background_failure_preserves_help", func(t *testing.T) {
		state["pending"], state["applied"], state["checked"], state["announced"] = nil, "", 0, false
		writeState()
		result := run("", nil)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("background failure interrupted help: %+v", result)
		}
		if got := waitForWorker(t, "checked"); got["verifier_unavailable"] != true {
			t.Fatal("unsupported verifier did not enable notice fallback")
		}
		current, err := os.ReadFile(binary)
		if err != nil || sha256.Sum256(current) != digest {
			t.Fatal("failed discovery changed installation")
		}
		writeNotice()
		result = run("", nil)
		if result.code != 0 || strings.TrimSpace(result.stderr) != updateNoticeMessage {
			t.Fatalf("unsupported verifier suppressed manual notice: %+v", result)
		}
	})
	if prefix := os.Getenv("OPENAI_CLI_TEST_HOMEBREW_PREFIX"); runtime.GOOS == "darwin" && prefix != "" {
		t.Run("enrolled_Homebrew_worker", func(t *testing.T) {
			// A runner-supplied Homebrew formula exercises native code signing
			// and the actual CLI. All discovery traffic stops at loopback.
			state = map[string]any{"schema": 1}
			writeState()
			enroll := exec.Command(binary, "__openai_homebrew_verifier", prefix)
			enroll.Env = append(append([]string{}, environment...), "OPENAI_CLI_ARCHIVE_INSTALL=1")
			if out, err := enroll.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "ready" {
				t.Fatalf("Homebrew enrollment: %s %v", out, err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"resp-test","object":"response","status":"completed","output":[]}`)
			}))
			defer server.Close()
			got := run("", []string{"OPENAI_BASE_URL=" + server.URL + "/v1", "OPENAI_API_KEY=sk-test-fixture"}, "responses", "create", "--model", "test-model", "--input", "test input")
			if got.code != 0 {
				t.Fatalf("ordinary request: %+v", got)
			}
			receipt := waitForWorker(t, "checked")
			if receipt["verifier_unavailable"] == true || receipt["homebrew_prefix"] != prefix {
				t.Fatal("worker lost enrolled verifier", receipt)
			}
		})
	}

}
