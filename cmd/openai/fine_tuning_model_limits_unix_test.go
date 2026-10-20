//go:build !windows

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainFineTuningModelLimitsNativeHelpExamples(t *testing.T) {
	work, binary := buildNativeShellCLI(t)
	executable := filepath.Join(work, binary)
	invocation := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	var requests, adminRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasPrefix(r.URL.Path, "/organization/") || strings.HasPrefix(r.URL.Path, "/admin/") {
			adminRequests.Add(1)
		}
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/fine_tuning/model_limits" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
		if r.Header.Get("Authorization") != "Bearer fake-native-shell-key" {
			t.Error("request omitted the ordinary synthetic credential")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			t.Errorf("read request has a body or read error: bytes=%d error=%v", len(body), err)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, fineTuningModelLimitsResponse); err != nil {
			t.Errorf("write synthetic limits response: %v", err)
		}
	}))
	defer server.Close()
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{"bash", "bash", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell is unavailable: %v", err)
				}
				t.Skipf("%s is not installed", shell.name)
			}
			shell.executable = path
			for _, lookup := range []string{"direct_path", "installed"} {
				t.Run(lookup, func(t *testing.T) {
					directory, home, searchPath := t.TempDir(), t.TempDir(), t.TempDir()
					printedInvocation := invocation
					if lookup == "direct_path" {
						if err := os.WriteFile(filepath.Join(searchPath, "openai"), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700); err != nil {
							t.Fatal(err)
						}
					} else {
						searchPath, printedInvocation = work, "openai"
					}
					t.Setenv("PATH", searchPath)
					for _, helpArgs := range []struct{ name, args string }{
						{"concise", " fine-tuning model-limits --help"},
						{"full", " help fine-tuning model-limits"},
					} {
						t.Run(helpArgs.name, func(t *testing.T) {
							requests.Store(0)
							adminRequests.Store(0)
							help := runNativeShell(t, shell, directory, home, server.URL, invocation+helpArgs.args)
							if help.code != 0 || help.stderr != "" || requests.Load() != 0 {
								t.Fatalf("help failed or made a request: %+v", help)
							}
							_, examples, found := strings.Cut(help.stdout, "EXAMPLES:\n")
							if !found {
								t.Fatal("help omitted executable examples")
							}
							examples, _, _ = strings.Cut(examples, "\nOPTIONS:")
							var commands []string
							for line := range strings.SplitSeq(examples, "\n") {
								if strings.HasPrefix(strings.TrimSpace(line), printedInvocation+" fine-tuning model-limits") {
									commands = append(commands, strings.TrimSpace(line))
								}
							}
							if len(commands) != 1 || commands[0] != printedInvocation+" fine-tuning model-limits" {
								t.Fatalf("help changed the example or executable identity %q:\n%s", printedInvocation, help.stdout)
							}
							got := runNativeShell(t, shell, directory, home, server.URL, shell.setKey+commands[0])
							if got.code != 0 || got.stderr != "" || requests.Load() != 1 || adminRequests.Load() != 0 {
								t.Fatalf("copied example failed: result=%+v requests=%d admin=%d", got, requests.Load(), adminRequests.Load())
							}
							for _, value := range []string{"synthetic-model", "9007199254740993", "0.100000000000000001", "synthetic-unfollowed-cursor"} {
								if strings.Count(got.stdout, value) != 1 {
									t.Fatalf("example output lost or duplicated %q: %q", value, got.stdout)
								}
							}
						})
					}
				})
			}
		})
	}
}
