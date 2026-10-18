package main

import (
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
)

func TestMainParserErrorGuidance(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"integer", []string{"models", "list", "--max-items", "synthetic-private-value"}, []string{"Invalid value for --max-items. Expected an integer, such as 1.", "openai models list --help"}},
		{"number", []string{"responses", "create", "--temperature", "synthetic-private-value"}, []string{"Invalid value for --temperature. Expected a number, such as 0.5.", "openai responses create --help"}},
		{"boolean", []string{"responses", "create", "--background", "synthetic-private-value"}, []string{"Invalid value for --background. Expected a boolean (true or false).", "openai responses create --help"}},
		{"poll duration", []string{"batches", "retrieve", "--poll-interval", "synthetic-private-value"}, []string{"Invalid value for --poll-interval. Expected a duration, such as 1s.", "openai batches retrieve --help"}},
		{"timeout duration", []string{"batches", "retrieve", "--wait-timeout", "synthetic-private-value"}, []string{"Invalid value for --wait-timeout. Expected a duration, such as 1s.", "openai batches retrieve --help"}},
		{"transposed flag", []string{"models", "list", "--max-itmes=synthetic-private-value"}, []string{"Did you mean --max-items?", "openai models list --help"}},
		{"inherited flag", []string{"models", "list", "--formta=synthetic-private-value"}, []string{"Did you mean --format?", "openai models list --help"}},
		{"missing value", []string{"models", "retrieve", "--model"}, []string{"Add a value for --model.", "openai models retrieve --help"}},
		{"required flag", []string{"embeddings", "create", "--input", "synthetic-private-input"}, []string{"Missing required options: --model.", "openai embeddings create --help"}},
		{"extra argument", []string{"models", "list", "synthetic-private-extra"}, []string{"Unexpected extra arguments.", "openai models list --help"}},
		{"private unknown", []string{"models", "list", "--synthetic-private-flag=synthetic-private-value"}, []string{"An option is not recognized.", "openai models list --help"}},
	} {
		for _, format := range []string{"text", "json", "jsonl"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				args := append([]string{"openai", "--base-url", server.URL, "--format-error", format}, test.args...)
				got := runMainDispatch(t, "bash", args...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("parser failure = %+v; want exit 1 and stderr only", got)
				}
				message := got.stderr
				if format != "text" {
					payload := decodeMainStructuredError(t, format, got.stderr)
					message, _ = payload["message"].(string)
					if _, exists := payload["status_code"]; exists {
						t.Errorf("local parser error invented an HTTP status: %v", payload)
					}
				}
				for _, want := range test.want {
					if !strings.Contains(message, want) {
						t.Errorf("message = %q, want %q", message, want)
					}
				}
				if strings.Contains(got.stderr, "synthetic-private-") {
					t.Errorf("diagnostic exposed rejected input: %q", got.stderr)
				}
			})
		}
	}
	if requests.Load() != 0 {
		t.Errorf("parser failures sent %d HTTP requests", requests.Load())
	}
}

func TestMainParserRecoveryNativeShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this recovery fixture exercises native Bash and zsh")
	}
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(work, "openai")
	copyShellTestCLI(t, binary)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "openai"), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other+string(os.PathListSeparator)+os.Getenv("PATH"))
	invocation := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{name: "bash", executable: "bash", args: []string{"--noprofile", "--norc", "-c"}},
		{name: "zsh", executable: "zsh", args: []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell is unavailable: %v", err)
				}
				t.Skipf("%s is unavailable", shell.name)
			}
			shell.executable = path
			dir := t.TempDir()
			for _, args := range []string{"models list --max-itmes=synthetic-private-value", "models list --max-items=synthetic-private-value", "models retrieve --model", "batches retrieve --poll-interval=synthetic-private-value"} {
				got := runNativeShell(t, shell, other, dir, "http://127.0.0.1:1", invocation+" "+args)
				_, recovery, found := strings.Cut(got.stderr, "Options and examples: ")
				recovery = strings.TrimSpace(recovery)
				if got.code != 1 || !found || !strings.HasPrefix(recovery, invocation+" ") || strings.Contains(got.stderr, "synthetic-private-") {
					t.Fatalf("recovery lost executable identity or privacy: %+v", got)
				}
				copied := runNativeShell(t, shell, other, dir, "not-a-url", recovery)
				if copied.code != 0 || copied.stderr != "" || !strings.Contains(copied.stdout, strings.Fields(args)[0]) {
					t.Fatalf("copied recovery failed: %+v", copied)
				}
			}
		})
	}
}
