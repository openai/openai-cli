package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMainBatchesOutputHelperRejectsInvalidInvocation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "private output helper must stay offline", http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []struct {
		name string
		args []string
	}{
		{"missing", nil},
		{"extra", []string{"3", "extra"}},
		{"empty", []string{""}},
		{"malformed", []string{"not-a-handle"}},
		{"negative", []string{"-1"}},
		{"stdin", []string{"0"}},
		{"stdout", []string{"1"}},
		{"stderr", []string{"2"}},
		{"uint64_overflow", []string{"18446744073709551616"}},
	}
	if strconv.IntSize == 32 {
		cases = append(cases, struct {
			name string
			args []string
		}{"uintptr_overflow", []string{"4294967296"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run main in an isolated process: a helper must never claim or close
			// a descriptor owned by this test process during malformed input checks.
			args := append([]string{"openai", "__batch-output"}, tc.args...)
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL}, args...)
			if got.code != 1 || got.stdout != "" || got.stderr != "" || requests.Load() != 0 {
				t.Fatalf("invalid helper invocation changed dispatch: result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

// Only test binaries recognize this sidecar. Production helper arguments and
// environment remain unchanged. Replacement finishes before any result bytes.
func runBatchDirectoryFixture(args []string) bool {
	if runtime.GOOS == "windows" || len(args) != 4 || args[1] != "__batch-directory" || args[3] != "3" {
		return false
	}
	path := args[2]
	mode, err := os.ReadFile(path + ".directory-fixture")
	if err != nil {
		return false
	}
	fail := func(err error) {
		if err != nil {
			os.Exit(99)
		}
	}
	fail(os.Mkdir(path, 0700))
	fail(os.Rename(path, path+".created"))
	fail(os.Rename(path+".replacement", path))
	fail(os.WriteFile(path+".helper-pid.tmp", []byte(strconv.Itoa(os.Getpid())), 0600))
	fail(os.Rename(path+".helper-pid.tmp", path+".helper-pid"))
	write := func(data []byte) {
		_, err := os.Stdout.Write(data)
		fail(err)
	}
	hang := func() {
		fail(os.WriteFile(path+".helper-phase", mode, 0600))
		time.Sleep(time.Hour)
		os.Exit(98)
	}
	var result [8]byte
	switch string(mode) {
	case "missing_ready":
		return true
	case "bad_ready":
		write([]byte{'X'})
		return true
	case "startup_hang":
		hang()
	}
	write([]byte{'R'})
	life := os.NewFile(3, "batch-directory-fixture-lifeline")
	if life == nil {
		os.Exit(99)
	}
	defer life.Close()
	var grant [1]byte
	_, err = io.ReadFull(life, grant[:])
	fail(err)
	if grant[0] != 'G' {
		os.Exit(99)
	}
	switch string(mode) {
	case "missing_result":
		return true
	case "truncated_result":
		write(result[:4])
		return true
	case "result_hang":
		hang()
	case "operation_delay":
		time.Sleep(2500 * time.Millisecond)
	case "partial_hang":
		write(result[:1])
		hang()
	case "invalid_errno":
		binary.LittleEndian.PutUint64(result[:], ^uint64(0))
	case "mkdir_error":
		binary.LittleEndian.PutUint64(result[:], uint64(syscall.EACCES))
	case "eexist_exit_failure":
		binary.LittleEndian.PutUint64(result[:], uint64(syscall.EEXIST))
	}
	write(result[:])
	switch string(mode) {
	case "trailing_data":
		write([]byte{'X'})
	case "exit_failure", "eexist_exit_failure":
		os.Exit(7)
	case "final_hang":
		hang()
	case "exit_hang":
		fail(os.Stdout.Close())
		hang()
	}
	return true
}

func batchDirectoryFixture(t *testing.T, mode string, sentinel bool) (string, os.FileInfo) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "results")
	replacement := directory + ".replacement"
	if err := os.Mkdir(replacement, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(replacement, 0770); err != nil {
		t.Fatal(err)
	}
	if sentinel {
		if err := os.WriteFile(filepath.Join(replacement, "sentinel"), []byte("preserve unrelated bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory+".directory-fixture", []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
	return directory, info
}

func assertBatchDirectoryReplacement(t *testing.T, directory string, original os.FileInfo, success bool) {
	t.Helper()
	info, err := os.Stat(directory)
	if err != nil || !os.SameFile(info, original) || info.Mode().Perm() != 0770 {
		t.Fatalf("replacement identity or permissions changed: info=%v error=%v", info, err)
	}
	entries, err := os.ReadDir(directory)
	want := 0
	if success {
		want = 3
		data, err := os.ReadFile(filepath.Join(directory, "sentinel"))
		if err != nil || string(data) != "preserve unrelated bytes" {
			t.Fatalf("replacement sentinel changed: bytes=%q error=%v", data, err)
		}
		for _, name := range []string{"output.jsonl", "error.jsonl"} {
			data, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil || string(data) != batchesWorkflowContent {
				t.Fatalf("saved bytes changed: file=%s bytes=%q error=%v", name, data, err)
			}
			fileInfo, err := os.Stat(filepath.Join(directory, name))
			if err != nil || fileInfo.Mode().Perm() != 0600 {
				t.Fatalf("saved file is not private: file=%s info=%v error=%v", name, fileInfo, err)
			}
		}
	}
	if err != nil || len(entries) != want {
		t.Fatalf("replacement contents changed: entries=%v error=%v; want %d", entries, err, want)
	}
	if created, err := os.ReadDir(directory + ".created"); err != nil || len(created) != 0 {
		t.Fatalf("original created directory changed: entries=%v error=%v", created, err)
	}
}

func batchDirectoryHelperPID(t *testing.T, directory string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(directory + ".helper-pid"); err == nil {
			pid, err := strconv.Atoi(string(data))
			if err != nil || pid <= 0 {
				t.Fatalf("invalid owned helper PID: %q (%v)", data, err)
			}
			return pid
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("directory helper did not reach the substitution barrier")
	return 0
}

func assertBatchDirectoryHelperReaped(t *testing.T, directory string) {
	t.Helper()
	pid := batchDirectoryHelperPID(t, directory)
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Release()
	if err := process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("directory helper %d was not reaped: %v", pid, err)
	}
}

func waitBatchDirectoryHelperPhase(t *testing.T, directory, phase string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(directory + ".helper-phase"); err == nil && string(data) == phase {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("directory helper did not enter %s", phase)
}

func TestMainBatchesDirectoryHelperSubstitutionAndProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory helper is not used on Windows")
	}
	for _, mode := range []string{"success", "operation_delay", "mkdir_error", "missing_ready", "bad_ready", "missing_result", "truncated_result", "invalid_errno", "trailing_data", "exit_failure", "eexist_exit_failure", "startup_hang", "partial_hang", "final_hang", "exit_hang"} {
		t.Run(mode, func(t *testing.T) {
			success := mode == "success" || mode == "operation_delay"
			directory, original := batchDirectoryFixture(t, mode, success)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch r.URL.Path {
				case "/batches/batch_synthetic":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, batchesWorkflowResponse("completed", batchesWorkflowCounts))
				case "/files/file_output/content", "/files/file_error/content":
					fmt.Fprint(w, batchesWorkflowContent)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			child := batchesWorkflowProcess(t, ctx, server, "batches", "wait", "batch_synthetic", "--output-dir", directory, "--format", "json")
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			started := time.Now()
			err := child.Run()
			if ctx.Err() != nil || time.Since(started) > 5*time.Second {
				t.Fatalf("directory helper exceeded its lifecycle bound: %v", err)
			}
			if success {
				if err != nil || requests.Load() != 3 || !json.Valid(stdout.Bytes()) || stderr.Len() != 0 {
					t.Fatalf("collection failed: error=%v requests=%d stdout=%q stderr=%q", err, requests.Load(), stdout.String(), stderr.String())
				}
			} else {
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 1 || requests.Load() != 0 || stdout.Len() != 0 || !json.Valid(stderr.Bytes()) {
					t.Fatalf("invalid helper result reached HTTP or succeeded: error=%v requests=%d stdout=%q stderr=%q", err, requests.Load(), stdout.String(), stderr.String())
				}
			}
			assertBatchDirectoryReplacement(t, directory, original, success)
			assertBatchDirectoryHelperReaped(t, directory)
		})
	}
}

func TestMainBatchesDirectoryHelperCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory helper and process signals are not used on Windows")
	}
	for _, phase := range []string{"startup_hang", "result_hang", "partial_hang"} {
		for _, stop := range []string{"deadline", "interrupt", "terminate"} {
			t.Run(phase+"/"+stop, func(t *testing.T) {
				directory, original := batchDirectoryFixture(t, phase, false)
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					http.Error(w, "preparation must finish before HTTP", http.StatusInternalServerError)
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
				defer cancel()
				args := []string{"batches", "wait", "batch_synthetic", "--output-dir", directory, "--format", "json"}
				wantCode := 124
				if stop == "deadline" {
					args = append(args, "--wait-timeout", "1s")
				}
				child := batchesWorkflowProcess(t, ctx, server, args...)
				var stdout, stderr bytes.Buffer
				child.Stdout, child.Stderr = &stdout, &stderr
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				waited := false
				defer func() {
					if !waited {
						_ = child.Process.Kill()
						_ = child.Wait()
					}
				}()
				waitBatchDirectoryHelperPhase(t, directory, phase)
				if stop != "deadline" {
					var signal os.Signal = os.Interrupt
					wantCode = 130
					if stop == "terminate" {
						signal, wantCode = syscall.SIGTERM, 143
					}
					if err := child.Process.Signal(signal); err != nil {
						t.Fatal(err)
					}
				}
				stopped := time.Now()
				err := child.Wait()
				waited = true
				exit, ok := err.(*exec.ExitError)
				if ctx.Err() != nil || time.Since(stopped) > 3*time.Second || !ok || exit.ExitCode() != wantCode || requests.Load() != 0 || stdout.Len() != 0 || !json.Valid(stderr.Bytes()) {
					t.Fatalf("directory cancellation: error=%v want=%d requests=%d stdout=%q stderr=%q", err, wantCode, requests.Load(), stdout.String(), stderr.String())
				}
				assertBatchDirectoryReplacement(t, directory, original, false)
				assertBatchDirectoryHelperReaped(t, directory)
			})
		}
	}
}

func TestMainBatchesDirectoryHelperDeadlineBeforeLaunch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory helper is not used on Windows")
	}
	directory, original := batchDirectoryFixture(t, "success", false)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "expired preparation must stay offline", http.StatusInternalServerError)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "batches", "wait", "batch_synthetic", "--output-dir", directory, "--wait-timeout", "1ns", "--format", "json")
	if got.code != 124 || got.stdout != "" || !json.Valid([]byte(got.stderr)) || requests.Load() != 0 {
		t.Fatalf("expired preparation: result=%+v requests=%d", got, requests.Load())
	}
	for _, path := range []string{directory, directory + ".helper-pid", directory + ".created"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired operation launched the helper: path=%s error=%v", path, err)
		}
	}
	info, err := os.Stat(directory + ".replacement")
	if err != nil || !os.SameFile(original, info) || info.Mode().Perm() != 0770 {
		t.Fatalf("expired operation changed replacement: info=%v error=%v", info, err)
	}
}

func TestMainBatchesDirectoryHelperRejectsInvalidInvocation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "private directory helper must stay offline", http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing_path", nil},
		{"missing_lifeline", []string{"{path}"}},
		{"extra", []string{"{path}", "3", "extra"}},
		{"empty_path", []string{"", "3"}},
		{"malformed", []string{"{path}", "not-a-handle"}},
		{"negative", []string{"{path}", "-1"}},
		{"stdin", []string{"{path}", "0"}},
		{"stdout", []string{"{path}", "1"}},
		{"stderr", []string{"{path}", "2"}},
		{"closed", []string{"{path}", "9999"}},
		{"overflow", []string{"{path}", "18446744073709551616"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "must-not-exist")
			args := []string{"openai", "__batch-directory"}
			for _, arg := range tc.args {
				args = append(args, strings.ReplaceAll(arg, "{path}", directory))
			}
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL}, args...)
			if got.code != 1 || got.stdout != "" || got.stderr != "" || requests.Load() != 0 {
				t.Fatalf("invalid directory helper changed dispatch: result=%+v requests=%d", got, requests.Load())
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("invalid directory helper changed filesystem: %v", err)
			}
		})
	}
}

func TestMainBatchesDirectoryHelperRejectsInvalidDescriptors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix private helper descriptors are not used on Windows")
	}
	for _, mode := range []string{"regular_lifeline", "write_lifeline", "read_stdout", "aliased_pipes"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			directory := filepath.Join(parent, "must-not-exist")
			lifeReader, lifeWriter, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer lifeReader.Close()
			defer lifeWriter.Close()
			replyReader, replyWriter, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer replyReader.Close()
			defer replyWriter.Close()
			lifeline, output := lifeReader, replyWriter
			switch mode {
			case "regular_lifeline":
				file, err := os.Create(filepath.Join(parent, "regular"))
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				lifeline = file
			case "write_lifeline":
				lifeline = lifeWriter
			case "read_stdout":
				output = replyReader
			case "aliased_pipes":
				lifeline = replyReader
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "__batch-directory", directory, "3")
			child.Env = []string{"GOMAXPROCS=2", "OPENAI_BASE_URL=http://127.0.0.1:1"}
			child.ExtraFiles = []*os.File{lifeline}
			child.Stdout = output
			var stderr bytes.Buffer
			child.Stderr = &stderr
			err = child.Run()
			exit, ok := err.(*exec.ExitError)
			if ctx.Err() != nil || !ok || exit.ExitCode() != 1 || stderr.Len() != 0 {
				t.Fatalf("invalid descriptor invocation: error=%v stderr=%q", err, stderr.String())
			}
			if err := replyWriter.Close(); err != nil {
				t.Fatal(err)
			}
			stdout, err := io.ReadAll(replyReader)
			if err != nil || len(stdout) != 0 {
				t.Fatalf("invalid descriptors emitted readiness: bytes=%q error=%v", stdout, err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("invalid descriptors created a directory: %v", err)
			}
		})
	}
}
