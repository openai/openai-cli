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
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type nativeShell struct {
	name, executable, binary, setKey string
	args                             []string
}

// These checks execute installed shells and the built CLI. They do not cover
// terminal-app rendering, interactive hidden input, or installed completion.
func TestMainNativeShell(t *testing.T) {
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := "openai"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	build := exec.CommandContext(ctx, goBinary, "build", "-o", filepath.Join(work, binary), ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	shells := []nativeShell{
		{"bash", "bash", "./openai", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "./openai", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"-f", "-c"}},
	}
	if runtime.GOOS == "windows" {
		shells = []nativeShell{
			{"cmd", "cmd.exe", `.\openai.exe`, `set "OPENAI_API_KEY=fake-native-shell-key" && `, []string{"/D", "/C"}},
			{"powershell", "powershell.exe", `.\openai.exe`, `$env:OPENAI_API_KEY = 'fake-native-shell-key'; `, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
			{"pwsh", "pwsh.exe", `.\openai.exe`, `$env:OPENAI_API_KEY = 'fake-native-shell-key'; `, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
		}
	}
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, name := range required {
		if name != "" && !slices.ContainsFunc(shells, func(shell nativeShell) bool { return shell.name == name }) {
			t.Fatalf("required shell %q is not configured on %s", name, runtime.GOOS)
		}
	}
	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell %s is unavailable: %v", shell.name, err)
				}
				t.Skipf("%s is not installed", shell.executable)
			}
			shell.executable = path
			home := t.TempDir()
			for _, flag := range []string{"-h", "--help", "--h"} {
				for _, command := range []string{"", "images generate "} {
					t.Run(command+flag, func(t *testing.T) {
						got := runNativeShell(t, shell, work, home, "not-a-url", shell.binary+" "+command+flag)
						if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "help --all") {
							t.Fatalf("help failed: %+v", got)
						}
					})
				}
			}
			for _, tc := range []struct{ args, want string }{
				{"help --all images generate", "--output-compression"},
				{"help setup", "OPENAI_API_KEY"},
			} {
				t.Run(tc.args, func(t *testing.T) {
					got := runNativeShell(t, shell, work, home, "not-a-url", shell.binary+" "+tc.args)
					if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, tc.want) {
						t.Fatalf("help failed: %+v", got)
					}
				})
			}
			for _, elsewhere := range []bool{false, true} {
				name, directory, invocation := "copy displayed command", work, shell.binary
				if elsewhere {
					name += " from another directory"
					directory = t.TempDir()
					path := filepath.Join(work, binary)
					if shell.name == "cmd" {
						invocation = `"` + path + `"`
					} else {
						invocation = "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
						if shell.name == "powershell" || shell.name == "pwsh" {
							invocation = "& '" + strings.ReplaceAll(path, "'", "''") + "'"
						}
					}
				}
				t.Run(name, func(t *testing.T) {
					short := runNativeShell(t, shell, directory, home, "not-a-url", invocation+" images generate --help")
					var command string
					for line := range strings.SplitSeq(short.stdout, "\n") {
						if after, ok := strings.CutPrefix(strings.TrimSpace(line), "Full help: "); ok {
							command = after
							break
						}
					}
					if short.code != 0 || command == "" {
						t.Fatalf("short help lacks a copyable full-help command: %+v", short)
					}
					got := runNativeShell(t, shell, directory, home, "not-a-url", command)
					if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "--output-compression") {
						t.Fatalf("displayed command %q failed: %+v", command, got)
					}
				})
			}
			t.Run("copy image example", func(t *testing.T) {
				short := runNativeShell(t, shell, work, home, "not-a-url", shell.binary+" images generate --help")
				var command string
				for line := range strings.SplitSeq(short.stdout, "\n") {
					if strings.Contains(line, " images generate --model ") {
						command = strings.TrimSpace(line)
						break
					}
				}
				if short.code != 0 || command == "" {
					t.Fatalf("short help lacks an image example: %+v", short)
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/images/generations" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					var body struct{ Model, Prompt string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode synthetic request: %v", err)
					} else if body.Model != "gpt-image-1.5" || body.Prompt != "A tiny orange robot" {
						t.Errorf("copied example changed its arguments: %+v", body)
					}
					if r.Header.Get("Authorization") != "Bearer fake-native-shell-key" {
						t.Error("shell environment did not supply the synthetic key")
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"created":1,"data":[{"b64_json":"c3ludGhldGlj"}]}`)
				}))
				defer server.Close()
				got := runNativeShell(t, shell, work, home, server.URL, shell.setKey+command)
				if got.code != 0 || got.stderr != "" || requests.Load() != 1 || !strings.Contains(got.stdout, "c3ludGhldGlj") {
					t.Fatalf("copied image example failed: code=%d requests=%d stderr=%q", got.code, requests.Load(), got.stderr)
				}
				if strings.Contains(got.stdout+got.stderr, "fake-native-shell-key") {
					t.Error("output printed the synthetic credential")
				}
			})
			t.Run("redirect", func(t *testing.T) {
				got := runNativeShell(t, shell, work, home, "not-a-url", shell.binary+" --help > help-output.txt")
				if got.code != 0 || got.stdout != "" || got.stderr != "" {
					t.Fatalf("redirect failed: %+v", got)
				}
				output, err := os.ReadFile(filepath.Join(work, "help-output.txt"))
				// Windows PowerShell 5.1 writes redirected text as UTF-16LE.
				if err != nil || (!bytes.Contains(output, []byte("OpenAI CLI")) && !bytes.Contains(output, []byte("O\x00p\x00e\x00n\x00A\x00I\x00 \x00C\x00L\x00I\x00"))) {
					t.Fatalf("redirected help is missing: %v", err)
				}
			})
			t.Run("exit status", func(t *testing.T) {
				got := runNativeShell(t, shell, work, home, "http://127.0.0.1:1", shell.binary+" nonexistent-test-command")
				if got.code == 0 {
					t.Fatalf("failed CLI command returned success: %+v", got)
				}
			})
			for _, tc := range []struct{ args, path, response string }{
				{"models list", "/models", `{"object":"list","data":[{"id":"shell-test-model","object":"model","created":1,"owned_by":"test"}]}`},
				{`models retrieve --model "--help"`, "/models/--help", `{"id":"--help","object":"model","created":1,"owned_by":"test"}`},
			} {
				t.Run(tc.args, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						if r.Method != http.MethodGet || r.URL.Path != tc.path {
							t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						}
						if r.Header.Get("Authorization") != "Bearer fake-native-shell-key" {
							t.Error("shell environment did not supply the synthetic key")
						}
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, tc.response)
					}))
					defer server.Close()
					got := runNativeShell(t, shell, work, home, server.URL, shell.setKey+shell.binary+" --format json "+tc.args)
					if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
						t.Fatalf("shell request failed: code=%d requests=%d stderr=%q", got.code, requests.Load(), got.stderr)
					}
					if strings.Contains(got.stdout+got.stderr, "fake-native-shell-key") {
						t.Error("output printed the synthetic credential")
					}
				})
			}
		})
	}
}

func runNativeShell(t *testing.T, shell nativeShell, work, home, baseURL, script string) mainDispatchResult {
	t.Helper()
	if shell.name == "powershell" || shell.name == "pwsh" {
		script += "; exit $LASTEXITCODE"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := append(slices.Clone(shell.args), script)
	if shell.name == "cmd" {
		// cmd.exe does not follow Go's normal Windows argument unquoting.
		// A batch file keeps quotes in the shell script intact.
		const file = "native-shell.cmd"
		if err := os.WriteFile(filepath.Join(work, file), []byte("@echo off\r\n"+script+"\r\nexit /b %errorlevel%\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(slices.Clone(shell.args), file)
	}
	child := exec.CommandContext(ctx, shell.executable, args...)
	child.Dir = work
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "OPENAI_") || slices.Contains([]string{"HOME", "USERPROFILE", "ZDOTDIR", "BASH_ENV", "ENV", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "PAGER", "TERM", "NO_COLOR"}, name) {
			continue
		}
		child.Env = append(child.Env, entry)
	}
	child.Env = append(child.Env, "HOME="+home, "USERPROFILE="+home, "ZDOTDIR="+home,
		"OPENAI_BASE_URL="+baseURL, "NO_COLOR=1", "TERM=dumb", "PAGER=cat")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err := child.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s timed out: %v", shell.name, ctx.Err())
	}
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return mainDispatchResult{code, stdout.String(), stderr.String()}
}
