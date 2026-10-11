package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const adminInteractiveTerminalMessage = "Run setup admin in a terminal without pipes or redirection. Use help setup admin for noninteractive instructions."
const adminInteractiveOutputMessage = "Interactive admin setup requires text output. Use help setup admin for noninteractive instructions."
const adminInteractiveUnsupportedMessage = "Interactive admin setup is unavailable on this platform."
const adminInteractiveManualPrefix = "For manual setup, run: "

// Probe the public path so host-side fallback checks exercise the real capability gate.
func adminSetupUnavailable(t *testing.T) bool {
	t.Helper()
	got := runMainDispatch(t, "bash", "openai", "setup", "admin")
	if got.code != 3 || got.stdout != "" {
		t.Fatalf("setup capability probe = %+v, want exit 3 and no result", got)
	}
	switch strings.TrimSpace(got.stderr) {
	case adminInteractiveUnsupportedMessage + "\n" + adminInteractiveManualPrefix + "openai help setup admin":
		return true
	case adminInteractiveTerminalMessage:
		switch runtime.GOOS {
		case "darwin", "linux", "windows", "ios", "android":
			return false
		default:
			t.Fatalf("setup reached terminal validation on unsupported platform %s", runtime.GOOS)
			return false
		}
	default:
		t.Fatalf("setup capability probe returned an unexpected diagnostic: %q", got.stderr)
		return false
	}
}

func TestMainAdminInteractiveHelpIsOffline(t *testing.T) {
	env := []string{
		"OPENAI_BASE_URL=not-a-request-url",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem",
		"OPENAI_API_KEY=synthetic-project-key-never-display",
		"OPENAI_ADMIN_KEY=synthetic-admin-key-never-display",
	}
	for _, args := range [][]string{
		{"openai", "setup", "--help"},
		{"openai", "setup", "admin", "--help"},
		{"openai", "setup", "admin", "-h"},
		{"openai", "--debug", "setup", "admin", "--help"},
		{"openai", "--format", "json", "setup", "admin", "--help"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "admin") {
				t.Fatalf("interactive setup help depends on request configuration: %+v", got)
			}
			if strings.Contains(got.stdout+got.stderr, "never-display") {
				t.Error("interactive setup help disclosed an environment credential")
			}
		})
	}
}

func TestMainAdminInteractiveRootHelpRoutes(t *testing.T) {
	env := []string{
		"OPENAI_BASE_URL=not-a-request-url",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem",
		"OPENAI_API_KEY=synthetic-project-key-never-display",
		"OPENAI_ADMIN_KEY=synthetic-admin-key-never-display",
	}
	for _, args := range [][]string{
		{"openai", "setup"},
		{"openai", "setup", "--help"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "Set up admin access\n") {
				t.Fatalf("setup root did not show its offline help: %+v", got)
			}
			for _, rejected := range []string{"Full help", "help --all setup", "never-display"} {
				if strings.Contains(got.stdout, rejected) {
					t.Errorf("setup root contains misleading or private text %q", rejected)
				}
			}
			var entry, commandHelp, ordinaryHelp string
			for line := range strings.SplitSeq(got.stdout, "\n") {
				line = strings.TrimSpace(line)
				switch line {
				case "openai setup admin":
					entry = line
				case "openai setup admin --help":
					commandHelp = line
				case "openai help setup":
					ordinaryHelp = line
				}
			}
			if entry == "" || commandHelp == "" || ordinaryHelp == "" || !strings.Contains(got.stdout, "Show manual setup for an ordinary API key:") {
				t.Fatalf("setup root lacks distinct, labelled command and ordinary guide routes: %q", got.stdout)
			}
			details := runMainDispatchWithEnv(t, "bash", env, strings.Fields(commandHelp)...)
			if details.code != 0 || details.stderr != "" || !strings.HasPrefix(details.stdout, "Verify an admin key with hidden input\n") {
				t.Fatalf("displayed command-help route did not describe interactive admin setup: %+v", details)
			}
			ordinary := runMainDispatchWithEnv(t, "bash", env, strings.Fields(ordinaryHelp)...)
			if ordinary.code != 0 || ordinary.stderr != "" || !strings.Contains(ordinary.stdout, "read -rs OPENAI_API_KEY") || !strings.Contains(ordinary.stdout, "openai models list") {
				t.Fatalf("displayed ordinary guide route changed: %+v", ordinary)
			}
			if strings.Contains(details.stdout+ordinary.stdout, "never-display") {
				t.Error("a displayed help route disclosed an environment credential")
			}
		})
	}
}

func TestMainAdminInteractiveRejectsNonterminalInput(t *testing.T) {
	want := adminInteractiveTerminalMessage
	if adminSetupUnavailable(t) {
		want = adminInteractiveUnsupportedMessage + "\n" + adminInteractiveManualPrefix + "openai help setup admin"
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	sentinel := filepath.Join(home, ".zshrc")
	const settings = "# preserve existing shell settings\n"
	if err := os.WriteFile(sentinel, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"open pipe", "synthetic piped key"} {
		t.Run(input, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reader.Close(); writer.Close() })
			if input == "synthetic piped key" {
				if _, err := writer.WriteString("synthetic-piped-key-never-display\n"); err != nil {
					t.Fatal(err)
				}
				writer.Close()
			}
			env := []string{"HOME=" + home, "USERPROFILE=" + home, "OPENAI_BASE_URL=" + server.URL}
			got := runMainDispatchWithStdin(t, "bash", env, reader, "openai", "setup", "admin")
			if got.code != 3 || got.stdout != "" || strings.TrimSpace(got.stderr) != want {
				t.Fatalf("nonterminal setup = %+v, want %q and exit 3", got, want)
			}
			if strings.Contains(got.stderr, "never-display") || strings.Contains(got.stderr, "Paste") {
				t.Error("nonterminal setup read or prompted for a key")
			}
			if input == "synthetic piped key" {
				remaining, err := io.ReadAll(reader)
				if err != nil || string(remaining) != "synthetic-piped-key-never-display\n" {
					t.Error("nonterminal setup consumed the piped credential")
				}
			}
		})
	}
	if requests.Load() != 0 {
		t.Errorf("nonterminal setup sent %d requests", requests.Load())
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 1 {
		t.Fatalf("nonterminal setup changed the home directory: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != settings {
		t.Fatalf("nonterminal setup changed shell settings: %q, %v", data, err)
	}
}

func TestMainAdminInteractiveUnsupportedPlatform(t *testing.T) {
	if !adminSetupUnavailable(t) {
		t.Skip("interactive input is supported on this platform")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	sentinel := filepath.Join(home, ".zshrc")
	const settings = "# preserve existing shell settings\n"
	if err := os.WriteFile(sentinel, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(home, "unread-input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	const content = "synthetic-unsupported-key-never-display\n"
	if _, err := input.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_BASE_URL=" + server.URL, "OPENAI_ADMIN_KEY=synthetic-environment-key-never-display"}
	for _, test := range []struct {
		name  string
		flags []string
	}{
		{"default", nil},
		{"quiet", []string{"--quiet"}},
		{"verbose", []string{"--verbose"}},
		{"quiet verbose", []string{"--quiet", "--verbose"}},
		{"invalid destination", []string{"--base-url", "not-a-request-url"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"openai"}, test.flags...)
			args = append(args, "setup", "admin")
			got := runMainDispatchWithStdin(t, "bash", env, input, args...)
			want := adminInteractiveUnsupportedMessage + "\n" + adminInteractiveManualPrefix + "openai help setup admin"
			code, message := 3, strings.TrimSpace(got.stderr)
			if test.name == "verbose" {
				lines := strings.SplitN(message, "\n", 5)
				if len(lines) != 5 || lines[0] != "Command: setup admin" || lines[1] != "Format option: auto" ||
					!strings.HasPrefix(lines[2], "Elapsed: ") || lines[3] != "Command result: failed" {
					t.Fatalf("unsupported setup lost its verbose diagnostics: %q", got.stderr)
				}
				if duration, err := time.ParseDuration(strings.TrimPrefix(lines[2], "Elapsed: ")); err != nil || duration < 0 {
					t.Fatalf("unsupported setup has invalid elapsed time: %q", lines[2])
				}
				message = lines[4]
			} else if test.name == "invalid destination" {
				code = 1
				want = "Invalid value for --base-url.\nOptions and examples: openai --help"
			}
			if got.code != code || got.stdout != "" || message != want {
				t.Fatalf("unsupported setup = %+v, want %q and exit %d", got, want, code)
			}
			for _, forbidden := range []string{"never-display", "Admin API key (hidden):", "Verifying admin access", "\x1b"} {
				if strings.Contains(got.stdout+got.stderr, forbidden) {
					t.Error("unsupported setup exposed input or started terminal interaction")
				}
			}
			if offset, err := input.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
				t.Error("unsupported setup consumed input")
			}
		})
	}
	if requests.Load() != 0 {
		t.Errorf("unsupported setup sent %d requests", requests.Load())
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 2 {
		t.Fatalf("unsupported setup changed the home directory: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != settings {
		t.Error("unsupported setup changed shell settings")
	}
	if data, err := os.ReadFile(input.Name()); err != nil || string(data) != content {
		t.Error("unsupported setup changed the input file")
	}
}

func TestMainAdminInteractiveErrorsPreserveFormats(t *testing.T) {
	wantText := adminInteractiveTerminalMessage
	if adminSetupUnavailable(t) {
		wantText = adminInteractiveUnsupportedMessage + "\n" + adminInteractiveManualPrefix + "openai help setup admin"
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	for _, test := range []struct {
		name, format string
		args         []string
	}{
		{"text", "text", nil},
		{"JSON", "json", []string{"--format-error", "json"}},
		{"JSONL", "jsonl", []string{"--format-error", "jsonl"}},
		{"raw", "raw", []string{"--format-error", "raw"}},
		{"YAML", "yaml", []string{"--format-error", "yaml"}},
		{"inherited JSON", "json", []string{"--format", "json"}},
		{"extracted JSON", "string", []string{"--format-error", "json", "--transform-error", "message"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"openai"}, test.args...)
			args = append(args, "setup", "admin")
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL}, args...)
			if got.code != 3 || got.stdout != "" || got.stderr == "" {
				t.Fatalf("noninteractive setup error = %+v", got)
			}
			message := strings.TrimSpace(got.stderr)
			switch test.format {
			case "text":
			case "string":
				if err := json.Unmarshal([]byte(got.stderr), &message); err != nil {
					t.Fatalf("setup error extraction is not JSON: %v; %q", err, got.stderr)
				}
			default:
				payload := decodeMainStructuredError(t, test.format, got.stderr)
				message, _ = payload["message"].(string)
				if _, exists := payload["status_code"]; exists {
					t.Errorf("local setup error invented an HTTP status: %#v", payload)
				}
			}
			want := adminInteractiveOutputMessage
			if test.format == "text" {
				want = wantText
			}
			if message != want {
				t.Errorf("setup error = %q, want %q", message, want)
			}
		})
	}
	if requests.Load() != 0 {
		t.Errorf("setup error modes sent %d requests", requests.Load())
	}
}

func TestMainAdminInteractiveRejectsKeyArguments(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			got := runMainDispatch(t, "bash", "openai", "--format-error", format, "setup", "admin", "synthetic-extra-key-never-display")
			if got.code != 3 || got.stdout != "" || got.stderr == "" || strings.Contains(got.stderr, "never-display") {
				t.Fatalf("setup key argument = %+v, want a safe nonzero error", got)
			}
			message := got.stderr
			if format == "json" {
				payload := decodeMainStructuredError(t, format, got.stderr)
				message, _ = payload["message"].(string)
			}
			if strings.TrimSpace(message) != "Run setup admin without extra arguments. Enter the key only at the hidden prompt." {
				t.Errorf("setup key argument lost the argument diagnostic: %q", message)
			}
		})
	}
}
