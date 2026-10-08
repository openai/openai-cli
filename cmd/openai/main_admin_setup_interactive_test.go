package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const adminInteractiveTerminalMessage = "Run setup admin in a terminal without pipes or redirection. Use help setup admin for noninteractive instructions."
const adminInteractiveOutputMessage = "Interactive admin setup requires text output. Use help setup admin for noninteractive instructions."

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
			if got.code != 3 || got.stdout != "" || strings.TrimSpace(got.stderr) != adminInteractiveTerminalMessage {
				t.Fatalf("nonterminal setup = %+v, want a terminal diagnostic and exit 3", got)
			}
			if strings.Contains(got.stderr, "never-display") || strings.Contains(got.stderr, "Paste") {
				t.Error("nonterminal setup read or prompted for a key")
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

func TestMainAdminInteractiveErrorsPreserveFormats(t *testing.T) {
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
				want = adminInteractiveTerminalMessage
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
