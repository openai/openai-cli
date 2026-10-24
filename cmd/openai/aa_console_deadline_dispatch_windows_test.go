//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Temporary dispatch entrypoint. Remove after the paired native experiment.
func TestMainDispatchConsoleDeadlineExperiment(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "openai.exe")
	runTokenizerRestorationHelper(t, "build", binary)
	productionHash := consoleDeadlineHash(t, binary)
	working, err := os.Getwd()
	consoleDeadlineCheck(t, "locate_working_directory", err)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	compiler, err := os.Executable()
	consoleDeadlineCheck(t, "locate_compile_helper", err)
	for _, variant := range []string{"baseline", "candidate"} {
		t.Run(variant, func(t *testing.T) {
			source := filepath.Join(working, "..", "..", "scripts", "console-deadline-"+variant+".go.txt")
			original := filepath.Join(working, "tokenizer_console_restoration_windows_test.go")
			overlay := filepath.Join(directory, variant+"-overlay.json")
			data, err := json.Marshal(map[string]any{"Replace": map[string]string{original: source}})
			consoleDeadlineCheck(t, "encode_native_overlay", err)
			consoleDeadlineCheck(t, "write_native_overlay", os.WriteFile(overlay, data, 0600))
			helper := filepath.Join(directory, variant+".test.exe")
			buildCtx, stopBuild := context.WithTimeout(ctx, 2*time.Minute)
			build := exec.CommandContext(buildCtx, compiler, "-test.v", "-test.run=^TestConsoleDeadlineCompileProcess$", "--", overlay, helper, filepath.Join(directory, variant+"-compiler"))
			build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOMAXPROCS=2")
			build.WaitDelay = 3 * time.Second
			var buildOut, buildErr adminConsoleCapture
			build.Stdout, build.Stderr = &buildOut, &buildErr
			err = build.Run()
			stopBuild()
			t.Logf("console-deadline compile_diagnostics=%q", buildOut.checkedText(t))
			if buildErr.checkedText(t) != "" {
				t.Error("compile owner produced unexpected stderr")
			}
			consoleDeadlineCheck(t, "compile_"+variant, err)
			output := filepath.Join(directory, variant+"-evidence")
			run := exec.CommandContext(ctx, helper, "-test.v", "-test.run=^TestConsoleDeadlineExperiment$", "-test.timeout=3m", "--", variant, binary, output)
			run.WaitDelay = 3 * time.Second
			var stdout, stderr adminConsoleCapture
			run.Stdout, run.Stderr = &stdout, &stderr
			err = run.Run()
			t.Logf("console-deadline variant=%s fixture_sha256=%s helper_sha256=%s production_sha256=%s context=%v", variant, consoleDeadlineHash(t, source), consoleDeadlineHash(t, helper), productionHash, ctx.Err())
			// Only fixed, synthetic evidence leaves the owned temporary directory.
			for _, mode := range []string{"delayed", "never_release", "never_ready", "descendant", "ordinary"} {
				for _, name := range []string{"original-tests.jsonl", "owner.json", "owner_finished.json", "child.json", "descendant.json", "gate_entered.json", "deadline_observed.json", "release.json", "gate_released.json", "start_returned.json", "input.json", "cleanup_returned.json", "accepted.json"} {
					path := filepath.Join(output, mode, name)
					data, readErr := os.ReadFile(path)
					if os.IsNotExist(readErr) {
						continue
					}
					consoleDeadlineCheck(t, "read_native_evidence", readErr)
					if len(data) > 1<<20 {
						t.Fatal("native evidence exceeded bound")
					}
					parts := (len(data) + 5999) / 6000
					for start, part := 0, 1; start < len(data); start, part = start+6000, part+1 {
						t.Logf("console-deadline artifact=%s/%s/%s part=%d total=%d sha256=%s data=%s", variant, mode, name, part, parts, consoleDeadlineHash(t, path), base64.StdEncoding.EncodeToString(data[start:min(start+6000, len(data))]))
					}
				}
			}
			// The experiment helper emits only synthetic diagnostics.
			t.Logf("console-deadline helper_output=%q", stdout.checkedText(t))
			if stderr.checkedText(t) != "" {
				t.Error("console experiment helper produced unexpected stderr")
			}
			consoleDeadlineCheck(t, "execute_"+variant, err)
			if consoleDeadlineHash(t, binary) != productionHash {
				t.Fatal("shared production binary changed")
			}
		})
	}
}

// The isolated compile owner contains compiler and linker descendants.
func TestConsoleDeadlineCompileProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 3 {
		t.Fatal("invalid compile owner arguments")
	}
	ownWindowsTestProcessTree(t)
	ctx, cancel := context.WithTimeout(t.Context(), 110*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "test", "-p", "2", "-overlay", args[0], "-c", "-o", args[1], ".")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOMAXPROCS=2")
	command.WaitDelay = 3 * time.Second
	var stdout, stderr adminConsoleCapture
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	out, diagnostic := stdout.checkedText(t), stderr.checkedText(t)
	consoleDeadlineCheck(t, "save_private_compile_stdout", os.WriteFile(args[2]+".stdout", []byte(out), 0600))
	consoleDeadlineCheck(t, "save_private_compile_stderr", os.WriteFile(args[2]+".stderr", []byte(diagnostic), 0600))
	pattern := regexp.MustCompile(`[A-Za-z0-9_]+\.go:[0-9]+:[0-9]+: [^\r\n]{1,400}`)
	for _, line := range pattern.FindAllString(out+"\n"+diagnostic, 30) {
		t.Logf("console-deadline compiler_diagnostic=%q", line)
	}
	for _, reason := range []string{"signal: killed", "out of memory", "context deadline exceeded", "no space left on device"} {
		if strings.Contains(out+diagnostic, reason) {
			t.Logf("console-deadline compiler_reason=%q", reason)
		}
	}
	t.Logf("console-deadline compiler_context=%s stdout_bytes=%d stderr_bytes=%d error_type=%T", consoleDeadlineContext(ctx), len(out), len(diagnostic), err)
	consoleDeadlineCheck(t, "owned_compilation", err)
}
