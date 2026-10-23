//go:build darwin || linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	noticeDirectory := filepath.Join(home, "cache", "openai-cli", "update-notice")
	if runtime.GOOS == "darwin" {
		noticeDirectory = filepath.Join(home, "Library", "Caches", "openai-cli", "update-notice")
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
		if first.code != 0 || second.code != 0 || first.stdout != second.stdout {
			t.Fatal("updater changed help or exit status")
		}
		if strings.TrimSpace(first.stderr) != message || strings.Contains(second.stderr, "Updated to") {
			t.Fatalf("confirmation not delivered once: first=%q second=%q", first.stderr, second.stderr)
		}
		if !strings.Contains(first.stdout, "openai") {
			t.Fatal("confirmation appeared without help")
		}
	})
	t.Run("background_failure_preserves_help", func(t *testing.T) {
		state["pending"], state["applied"], state["checked"], state["announced"] = nil, "", 0, false
		writeState()
		result := run("", nil)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("background failure interrupted help: %+v", result)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			data, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Checked             int64
				VerifierUnavailable bool `json:"verifier_unavailable"`
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Checked > 0 {
				if !got.VerifierUnavailable {
					t.Fatal("unsupported verifier did not enable notice fallback")
				}
				// State is reserved before probing. Wait for the worker's lock
				// release before testing the next independent invocation.
				lock, err := os.Open(filepath.Join(stateDirectory, ".lock"))
				if err != nil {
					t.Fatal(err)
				}
				err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
				lock.Close()
				if err == nil {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("detached helper did not reserve discovery")
			}
			time.Sleep(10 * time.Millisecond)
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
}
