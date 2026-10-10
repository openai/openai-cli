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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainExamplesOfflineOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	server, requests := localUtilitiesRequestTrap(t)
	for _, configuration := range []struct {
		name string
		env  []string
	}{
		{"no credentials", []string{"OPENAI_BASE_URL=" + server.URL}},
		{"invalid remote configuration", []string{
			"OPENAI_BASE_URL=://synthetic-private-endpoint",
			"OPENAI_CUSTOM_HEADERS=synthetic-private-headers",
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert",
			"HTTPS_PROXY=://synthetic-private-proxy",
		}},
	} {
		t.Run(configuration.name, func(t *testing.T) {
			env := append(localUtilitiesEnvironment(t), configuration.env...)
			for _, topic := range []string{"files", "audio", "models"} {
				t.Run(topic, func(t *testing.T) {
					want := runMainDispatchWithEnv(t, "bash", env, "openai", "examples", topic)
					require.Zero(t, want.code, "%s", want.stderr)
					require.Empty(t, want.stderr)
					require.Contains(t, want.stdout, "openai ")
					require.NotContains(t, want.stdout, "\x1b")
					for _, flags := range [][]string{
						{"--format", "auto"}, {"--format", "text"}, {"--quiet"}, {"--verbose"},
					} {
						args := append([]string{"openai", "examples", topic}, flags...)
						got := runMainDispatchWithEnv(t, "bash", append(env, "NO_COLOR=1", "COLUMNS=20", "TERM=dumb"), args...)
						require.Zero(t, got.code, "%s", got.stderr)
						require.Equal(t, want.stdout, got.stdout, "%v changed recipe bytes", flags)
						require.NotContains(t, got.stderr, "synthetic-private-")
					}
					for _, args := range [][]string{
						{"openai", "--format", "json", "examples", topic},
						{"openai", "examples", "--format", "JSON", topic},
						{"openai", "examples", topic, "--format", "json"},
					} {
						got := runMainDispatchWithEnv(t, "bash", env, args...)
						require.Zero(t, got.code, "%s", got.stderr)
						require.Empty(t, got.stderr)
						var result map[string]string
						require.NoError(t, json.Unmarshal([]byte(got.stdout), &result))
						require.Equal(t, map[string]string{"topic": topic, "shell": "sh", "script": want.stdout}, result)
					}
				})
			}
		})
	}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	require.Empty(t, entries, "printing recipes must not create files")
	require.Zero(t, requests.Load(), "printing recipes reached the API")
}

func TestMainExamplesHelpAndErrors(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL="+server.URL)
	for _, args := range [][]string{
		{"examples"}, {"examples", "--help"}, {"help", "examples"},
		{"examples", "files", "--help"}, {"help", "examples", "audio"},
		{"--format", "json", "help", "examples", "models"},
		{"help", "examples", "models", "--format", "json"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "openai examples")
			require.NotContains(t, got.stdout, "Key setup:")
		})
	}
	for _, args := range [][]string{
		{"examples", "synthetic-private-topic\x1b[31m"},
		{"examples", "files", "synthetic-private-extra\nargument"},
		{"examples", "--format", "json"},
		{"examples", "models", "--format", "yaml"},
		{"examples", "models", "--format", "raw"},
		{"examples", "models", "--format", "jsonl"},
		{"examples", "files", "--transform", "synthetic-private-transform"},
		{"examples", "files", "--transform="},
		{"examples", "audio", "--raw-output"},
		{"examples", "audio", "--raw-output=false"},
		{"examples", "models", "--synthetic-private-flag"},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(strings.Join(args, "/")+"/"+format, func(t *testing.T) {
				argv := append([]string{"openai", "--format-error", format}, args...)
				got := runMainDispatchWithEnv(t, "bash", env, argv...)
				require.NotZero(t, got.code)
				require.Empty(t, got.stdout)
				require.NotEmpty(t, got.stderr)
				require.NotContains(t, got.stderr, "synthetic-private-")
				require.NotContains(t, got.stderr, "\x1b")
				if format == "json" {
					payload := decodeMainStructuredError(t, format, got.stderr)
					require.NotContains(t, payload, "status_code")
				}
			})
		}
	}
	require.Zero(t, requests.Load())
}

func TestMainExamplesDoNotReadStdin(t *testing.T) {
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint")
	for _, topic := range []string{"files", "audio", "models"} {
		t.Run(topic, func(t *testing.T) {
			input, output, err := os.Pipe()
			require.NoError(t, err)
			defer input.Close()
			defer output.Close()
			// Keep an empty pipe open. A stdin read would prevent completion.
			got := runMainDispatchWithStdin(t, "bash", env, input, "openai", "examples", topic)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "openai ")
		})
	}
}

func TestMainExamplesStdoutFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix read-only stdout descriptor behavior")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, args := range [][]string{{"examples"}, {"examples", "files"}, {"examples", "models", "--format", "json"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			// A read-only descriptor makes the actual stdout write fail without SIGPIPE.
			sink, err := os.Open(os.DevNull)
			require.NoError(t, err)
			defer sink.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			argv := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)
			child := exec.CommandContext(ctx, binary, argv...)
			child.Env = append(localUtilitiesEnvironment(t), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_BASE_URL=://invalid-remote-configuration", "FORCE_COLOR=0")
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = sink, &stderr
			err = child.Run()
			require.NoError(t, ctx.Err(), "stdout failure must complete")
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.NotZero(t, exit.ExitCode())
			require.Contains(t, stderr.String(), "Could not write examples")
		})
	}
}

func TestMainExamplesScriptsRunHelp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/run uses a POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	server, requests := localUtilitiesRequestTrap(t)
	decoy := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(decoy, "openai"), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700))
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "OPENAI_") && !slices.Contains([]string{"BASH_ENV", "ENV", "PATH", "GOTOOLCHAIN", "GOMAXPROCS", "GOFLAGS", "FORCE_COLOR", "NO_COLOR"}, name) {
			env = append(env, entry)
		}
	}
	env = append(env, "OPENAI_BASE_URL="+server.URL, "GOTOOLCHAIN=local", "GOMAXPROCS=2", "GOFLAGS=-p=2", "FORCE_COLOR=0", "NO_COLOR=1",
		"PATH="+strings.Join([]string{filepath.Join(runtime.GOROOT(), "bin"), decoy, os.Getenv("PATH")}, string(os.PathListSeparator)))
	run := func(t *testing.T, executable string, args ...string) string {
		t.Helper()
		child := exec.CommandContext(ctx, executable, args...)
		child.Dir, child.Env = root, env
		var stdout, stderr bytes.Buffer
		child.Stdout, child.Stderr = &stdout, &stderr
		err := child.Run()
		require.NoError(t, err, "%s %v: %s", executable, args, stderr.String())
		require.Empty(t, stderr.String())
		return stdout.String()
	}
	want := runMainDispatchWithEnv(t, "bash", append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL="+server.URL), "openai", "examples", "files")
	require.Zero(t, want.code, "%s", want.stderr)
	require.Empty(t, want.stderr)
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	// Repeat both help routes to cover Go's fresh and cached executable paths.
	for _, attempt := range []string{"first", "repeat"} {
		for _, args := range [][]string{{"examples", "--help"}, {"help", "examples", "files"}} {
			output := run(t, "./scripts/run", args...)
			var command string
			for line := range strings.SplitSeq(output, "\n") {
				if strings.TrimSpace(line) == "go run ./cmd/openai examples files" {
					command = strings.TrimSpace(line)
					break
				}
			}
			require.NotEmpty(t, command, "source help lost its copyable invocation: %s", output)
			for _, shell := range []nativeShell{
				{name: "bash", executable: "bash", args: []string{"--noprofile", "--norc", "-c"}},
				{name: "zsh", executable: "zsh", args: []string{"-f", "-c"}},
			} {
				name := strings.Join(args, "/") + "/" + shell.name + "/" + attempt
				t.Run(name, func(t *testing.T) {
					path, err := exec.LookPath(shell.executable)
					if err != nil {
						if slices.Contains(required, shell.name) {
							t.Fatalf("required shell %s is unavailable: %v", shell.name, err)
						}
						t.Skipf("%s is not installed", shell.name)
					}
					copied := run(t, path, append(slices.Clone(shell.args), command)...)
					require.Equal(t, want.stdout, copied)
				})
			}
		}
	}
	require.Zero(t, requests.Load(), "copied source help commands must stay offline")
}

// Native recipes use the printed script itself, with only documented paths replaced.
func TestMainExamplesNativeRecipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("these recipes explicitly target POSIX shells")
	}
	binDir := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	require.NoError(t, os.Mkdir(binDir, 0o700))
	binary := filepath.Join(binDir, "openai")
	if existing := os.Getenv("OPENAI_CLI_EXAMPLES_BINARY"); existing != "" {
		absolute, err := filepath.Abs(existing)
		require.NoError(t, err)
		require.NoError(t, os.Symlink(absolute, binary))
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-p=2", "-o", binary, ".")
		build.Env = append(os.Environ(), "GOMAXPROCS=2")
		output, err := build.CombinedOutput()
		require.NoError(t, err, "build CLI: %s", output)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FORCE_COLOR", "0")
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	shells := []nativeShell{
		{name: "bash", executable: "bash", args: []string{"--noprofile", "--norc", "-c"}},
		{name: "zsh", executable: "zsh", args: []string{"-f", "-c"}},
	}
	for _, name := range required {
		require.True(t, name == "" || slices.ContainsFunc(shells, func(shell nativeShell) bool { return shell.name == name }),
			"required shell %q is not configured for POSIX recipes", name)
	}
	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell %s is unavailable: %v", shell.name, err)
				}
				t.Skipf("%s is not installed", shell.name)
			}
			shell.executable = path
			t.Run("copy short and full help", func(t *testing.T) {
				testExamplesHelpCommands(t, shell, binary)
			})
			for _, test := range []struct {
				name, failure string
				quoted        bool
			}{
				{name: "files"}, {name: "files quoted paths", quoted: true},
				{name: "upload failure", failure: "POST /files"},
				{name: "metadata failure", failure: "GET /files/file_recipe"},
			} {
				t.Run(test.name, func(t *testing.T) {
					testExamplesFilesRecipe(t, shell, test.quoted, test.failure)
				})
			}
			for _, quoted := range []bool{false, true} {
				name := "audio"
				if quoted {
					name += " quoted path"
				}
				t.Run(name, func(t *testing.T) { testExamplesAudioRecipe(t, shell, quoted) })
			}
			t.Run("models", func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/models" {
						t.Errorf("unexpected model request: %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"model_recipe_a","object":"model","owned_by":"synthetic","created":1},{"id":"model_recipe_b","object":"model","owned_by":"synthetic","created":2}]}`)
				}))
				defer server.Close()
				work, home := t.TempDir(), t.TempDir()
				script := examplesPrintedScript(t, shell, work, home, "models")
				got := runNativeShell(t, shell, work, home, server.URL, examplesFakeCredentials+script)
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				require.Equal(t, "model_recipe_a\nmodel_recipe_b\n", got.stdout)
				require.EqualValues(t, 1, requests.Load())
			})
		})
	}
}

const examplesFakeCredentials = "export OPENAI_API_KEY='sk-synthetic-examples-test'; "

func testExamplesHelpCommands(t *testing.T, shell nativeShell, binary string) {
	t.Helper()
	server, requests := localUtilitiesRequestTrap(t)
	for _, otherCLI := range []bool{false, true} {
		name := "empty PATH"
		if otherCLI {
			name = "different CLI on PATH"
		}
		t.Run(name, func(t *testing.T) {
			path := t.TempDir()
			if otherCLI {
				require.NoError(t, os.WriteFile(filepath.Join(path, "openai"), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700))
			}
			t.Setenv("PATH", path)
			work, home := t.TempDir(), t.TempDir()
			invocation := examplesShellQuote(binary)
			for _, suffix := range []string{" examples --help", " help examples", " examples files --help", " help examples files"} {
				t.Run(strings.TrimSpace(suffix), func(t *testing.T) {
					got := runNativeShell(t, shell, work, home, "://invalid-remote-configuration", invocation+suffix)
					require.Zero(t, got.code, "%s", got.stderr)
					require.Empty(t, got.stderr)
					var command string
					for line := range strings.SplitSeq(got.stdout, "\n") {
						if strings.TrimSpace(line) == invocation+" examples files" {
							command = strings.TrimSpace(line)
							break
						}
					}
					require.NotEmpty(t, command, "help must preserve the invoked executable: %s", got.stdout)
					copied := runNativeShell(t, shell, work, home, "://invalid-remote-configuration", command)
					require.Zero(t, copied.code, "%s", copied.stderr)
					require.Empty(t, copied.stderr)
					require.True(t, strings.HasPrefix(copied.stdout, "# POSIX shell."))
					require.Contains(t, copied.stdout, "openai --format json")
					require.NotContains(t, copied.stdout, "wrong executable")
				})
			}
			t.Run("copy index commands", func(t *testing.T) {
				var want string
				for _, flags := range []string{"", " --format auto", " --format text", " --quiet", " --verbose"} {
					got := runNativeShell(t, shell, work, home, server.URL, invocation+" examples"+flags)
					require.Zero(t, got.code, "%s", got.stderr)
					if flags != " --verbose" {
						require.Empty(t, got.stderr)
					}
					if want == "" {
						want = got.stdout
					}
					require.Equal(t, want, got.stdout, "index flags must not change command bytes")
				}
				lines := strings.Split(strings.TrimSuffix(want, "\n"), "\n")
				require.Len(t, lines, 6, "discovery must stay concise")
				var commands []string
				for _, line := range lines {
					if !strings.HasPrefix(line, "#") {
						commands = append(commands, line)
					}
				}
				require.Len(t, commands, 4)
				for index, topic := range []string{"files", "audio", "models", "--help"} {
					command, _, _ := strings.Cut(commands[index], " #")
					require.Equal(t, invocation+" examples "+topic, strings.TrimSpace(command))
					// Execute the complete displayed line, including its inline comment.
					copied := runNativeShell(t, shell, work, home, server.URL, commands[index])
					require.Zero(t, copied.code, "%s", copied.stderr)
					require.Empty(t, copied.stderr)
					if topic == "--help" {
						require.Contains(t, copied.stdout, "--format")
					} else {
						require.True(t, strings.HasPrefix(copied.stdout, "# POSIX shell."))
					}
					require.NotContains(t, copied.stdout, "wrong executable")
				}
			})
			for _, test := range []struct {
				name, recovery, topic string
				args                  []string
				json                  bool
			}{
				{"unknown topic", "examples", "", []string{"examples", "synthetic-private-topic'$(touch sentinel)\n\x1b[31m"}, false},
				{"extra argument", "examples", "", []string{"examples", "files", "synthetic-private-path'$(touch sentinel)"}, false},
				{"bare JSON", "examples files --format json", "files", []string{"examples", "--format", "json"}, true},
				{"unsupported format", "examples models --format text", "models", []string{"examples", "models", "--format", "yaml"}, false},
				{"transform", "examples files --format text", "files", []string{"examples", "files", "--transform", "synthetic-private-value'$(touch sentinel)"}, false},
				{"empty transform", "examples files --format text", "files", []string{"examples", "files", "--transform="}, false},
				{"raw output", "examples audio --format text", "audio", []string{"examples", "audio", "--raw-output"}, false},
				{"raw output false", "examples audio --format text", "audio", []string{"examples", "audio", "--raw-output=false"}, false},
			} {
				for _, format := range []string{"text", "json"} {
					t.Run("copy recovery/"+test.name+"/"+format, func(t *testing.T) {
						command := invocation + " --format-error " + format
						for _, arg := range test.args {
							command += " " + examplesShellQuote(arg)
						}
						got := runNativeShell(t, shell, work, home, server.URL, command)
						require.NotZero(t, got.code)
						require.Empty(t, got.stdout)
						require.NotContains(t, got.stderr, "synthetic-private-")
						require.NotContains(t, got.stderr, "\x1b")
						message := got.stderr
						if format == "json" {
							payload := decodeMainStructuredError(t, format, got.stderr)
							require.NotContains(t, payload, "status_code")
							message, _ = payload["message"].(string)
						}
						require.Equal(t, 1, strings.Count(message, "\nTry: "), "recovery must contain one command")
						_, recovery, found := strings.Cut(message, "\nTry: ")
						require.True(t, found)
						recovery = strings.TrimSpace(recovery)
						require.Equal(t, invocation+" "+test.recovery, recovery, "recovery must not replay rejected input")
						copied := runNativeShell(t, shell, work, home, server.URL, recovery)
						require.Zero(t, copied.code, "%s", copied.stderr)
						require.Empty(t, copied.stderr)
						require.NotContains(t, copied.stdout, "wrong executable")
						if test.json {
							var result map[string]string
							require.NoError(t, json.Unmarshal([]byte(copied.stdout), &result))
							require.Equal(t, test.topic, result["topic"])
							require.Equal(t, "sh", result["shell"])
							require.True(t, strings.HasPrefix(result["script"], "# POSIX shell."))
						} else if test.topic == "" {
							require.Contains(t, copied.stdout, "# Choose a recipe to print.")
						} else {
							require.True(t, strings.HasPrefix(copied.stdout, "# POSIX shell."))
						}
					})
				}
			}
			for _, directory := range []string{work, home} {
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Empty(t, entries, "discovery and recovery must not create files")
			}
		})
	}
	require.Zero(t, requests.Load(), "discovery and recovery must not call the API")
}

func examplesPrintedScript(t *testing.T, shell nativeShell, work, home, topic string) string {
	t.Helper()
	got := runNativeShell(t, shell, work, home, "://invalid-remote-configuration", "openai examples "+topic)
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.True(t, strings.HasPrefix(got.stdout, "# POSIX shell."), "recipe must identify its shell")
	return got.stdout
}

func examplesShellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

func testExamplesFilesRecipe(t *testing.T, shell nativeShell, quoted bool, failure string) {
	t.Helper()
	work, home := t.TempDir(), t.TempDir()
	input, output := "upload sample.txt", "downloaded copy.txt"
	if quoted {
		input, output = "input's \"$HOME\" $(touch sentinel).txt", "download's \"$HOME\" copy.txt"
	}
	payload := []byte("synthetic file\x00\xff\r\n")
	require.NoError(t, os.WriteFile(filepath.Join(work, input), payload, 0o600))
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := r.Method + " " + r.URL.Path
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if request == failure {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic recipe failure","type":"invalid_request_error"}}`)
			return
		}
		switch request {
		case "POST /files":
			assertExamplesUpload(t, r, input, payload, "purpose", "user_data")
			fallthrough
		case "GET /files/file_recipe":
			_, _ = io.WriteString(w, `{"id":"file_recipe","object":"file","bytes":18,"created_at":1,"filename":"synthetic.txt","purpose":"user_data"}`)
		case "GET /files/file_recipe/content":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(payload)
		default:
			t.Errorf("unexpected Files request: %s", request)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	script := examplesPrintedScript(t, shell, work, home, "files")
	if quoted {
		script = strings.ReplaceAll(script, `"./upload sample.txt"`, examplesShellQuote("./"+input))
		script = strings.ReplaceAll(script, `"./downloaded copy.txt"`, examplesShellQuote("./"+output))
	}
	got := runNativeShell(t, shell, work, home, server.URL, examplesFakeCredentials+script)
	want := []string{"POST /files", "GET /files/file_recipe", "GET /files/file_recipe/content"}
	if failure != "" {
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		if failure == "POST /files" {
			require.Contains(t, got.stderr, "synthetic recipe failure")
		} else {
			require.Equal(t, "HTTP 400: Bad Request.\nThe API rejected the request.\nOptions and examples: openai help files get\nAPI error details: --format-error json.\n", got.stderr)
		}
		_, err := os.Stat(filepath.Join(work, output))
		require.ErrorIs(t, err, os.ErrNotExist)
		want = want[:slices.Index(want, failure)+1]
	} else {
		require.Zero(t, got.code, "%s", got.stderr)
		require.Equal(t, "Full data: --format json.\nWrote output to: ./"+output+"\n", got.stderr)
		require.Contains(t, got.stdout, "file_recipe")
		content, err := os.ReadFile(filepath.Join(work, output))
		require.NoError(t, err)
		require.Equal(t, payload, content)
	}
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, want, requests, "later steps must stop after a failed command")
	entries, err := os.ReadDir(work)
	require.NoError(t, err)
	wantFiles := 2
	if failure != "" {
		wantFiles = 1
	}
	require.Len(t, entries, wantFiles, "quoted paths must not execute shell expansions")
}

func testExamplesAudioRecipe(t *testing.T, shell nativeShell, quoted bool) {
	t.Helper()
	work, home := t.TempDir(), t.TempDir()
	input := "speech sample.wav"
	if quoted {
		input = "audio's \"$HOME\" $(touch sentinel).wav"
	}
	payload := []byte("synthetic audio bytes\x00\xff")
	require.NoError(t, os.WriteFile(filepath.Join(work, input), payload, 0o600))
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertExamplesUpload(t, r, input, payload, "model", "whisper-1")
		request := r.Method + " " + r.URL.Path + " " + r.FormValue("response_format")
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		switch request {
		case "POST /audio/transcriptions text":
			_, _ = io.WriteString(w, "Synthetic transcript.\r\n")
		case "POST /audio/translations text":
			_, _ = io.WriteString(w, "Synthetic English translation.\n")
		case "POST /audio/transcriptions srt":
			w.Header().Set("Content-Type", "application/x-subrip")
			_, _ = io.WriteString(w, "1\n00:00:00,000 --> 00:00:01,000\nSynthetic subtitle.\n\n")
		default:
			t.Errorf("unexpected audio request: %s", request)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	script := examplesPrintedScript(t, shell, work, home, "audio")
	if quoted {
		script = strings.ReplaceAll(script, `"./speech sample.wav"`, examplesShellQuote("./"+input))
	}
	got := runNativeShell(t, shell, work, home, server.URL, examplesFakeCredentials+script)
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.Equal(t, "Synthetic transcript.\r\nSynthetic English translation.\n1\n00:00:00,000 --> 00:00:01,000\nSynthetic subtitle.\n\n", got.stdout)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"POST /audio/transcriptions text", "POST /audio/translations text", "POST /audio/transcriptions srt"}, requests)
	entries, err := os.ReadDir(work)
	require.NoError(t, err)
	require.Len(t, entries, 1, "audio recipes print results and must not execute path expansions")
}

func assertExamplesUpload(t *testing.T, r *http.Request, filename string, payload []byte, field, value string) {
	t.Helper()
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Errorf("parse synthetic upload: %v", err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	if r.FormValue(field) != value {
		t.Errorf("upload %s = %q, want %q", field, r.FormValue(field), value)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		t.Errorf("read synthetic upload: %v", err)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil || string(content) != string(payload) || header.Filename != filename {
		t.Errorf("synthetic upload changed: filename=%q, bytes=%q, error=%v", header.Filename, content, err)
	}
}
