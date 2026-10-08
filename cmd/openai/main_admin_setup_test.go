package main

import (
	"context"
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

func TestMainAdminSetupOfflineRoutes(t *testing.T) {
	env := []string{
		"OPENAI_BASE_URL=not-a-request-url",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem",
		"OPENAI_API_KEY=synthetic-project-key-never-display",
		"OPENAI_ADMIN_KEY=synthetic-admin-key-never-display",
	}
	for _, args := range [][]string{
		{"openai", "help", "setup", "admin"},
		{"openai", "help", "setup", "admin", "--help"},
		{"openai", "help", "setup", "admin", "-h"},
		{"openai", "help", "--all", "setup", "admin"},
		{"openai", "--debug", "help", "setup", "admin"},
		{"openai", "--format", "json", "help", "setup", "admin"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("admin setup depends on request configuration: %+v", got)
			}
			for _, want := range []string{
				"https://platform.openai.com/settings/organization/admin-keys",
				"organization owner", "OPENAI_ADMIN_KEY",
				"read -rs OPENAI_ADMIN_KEY", "export OPENAI_ADMIN_KEY",
				"-AsSecureString", "PowerShell",
				"openai --format text admin organization projects list",
				"unset OPENAI_ADMIN_KEY", "Remove-Item Env:OPENAI_ADMIN_KEY",
			} {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("admin setup lacks %q", want)
				}
			}
			if strings.Contains(got.stdout+got.stderr, "never-display") {
				t.Error("admin setup disclosed an environment credential")
			}
			if strings.Contains(got.stdout, "--admin-api-key") {
				t.Error("beginner setup invites entering credentials in command arguments")
			}
		})
	}
}

func TestMainAdminSetupIsReadOnly(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "setup help must not send requests", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	sentinel := filepath.Join(home, ".zshrc")
	const settings = "# preserve existing shell settings\n"
	if err := os.WriteFile(sentinel, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "OPENAI_BASE_URL=" + server.URL}
	// An open, empty pipe catches accidental prompts or credential reads.
	got := runMainDispatchWithStdin(t, "bash", env, reader, "openai", "help", "setup", "admin")
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "OPENAI_ADMIN_KEY") {
		t.Fatalf("read-only admin setup failed: %+v", got)
	}
	if requests.Load() != 0 {
		t.Fatalf("admin setup sent %d requests", requests.Load())
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 {
		t.Fatalf("admin setup changed the home directory: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != settings {
		t.Fatalf("admin setup changed shell settings: %q, %v", data, err)
	}
}

func TestMainAdminSetupPreservesOrdinarySetup(t *testing.T) {
	got := runMainDispatch(t, "bash", "openai", "help", "setup")
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "read -rs OPENAI_API_KEY") || !strings.Contains(got.stdout, "openai models list") {
		t.Fatalf("ordinary API key setup changed: %+v", got)
	}
}

func TestMainAdminSetupRejectsExtraArguments(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			got := runMainDispatch(t, "bash", "openai", "--format-error", format, "help", "setup", "admin", "synthetic-secret-extra")
			if got.code != 3 || got.stdout != "" || got.stderr == "" || strings.Contains(got.stderr, "synthetic-secret-extra") {
				t.Fatalf("invalid admin setup invocation = %+v, want safe error and exit 3", got)
			}
			message := strings.TrimSpace(got.stderr)
			if format == "json" {
				payload := decodeMainStructuredError(t, format, got.stderr)
				message, _ = payload["message"].(string)
				if _, exists := payload["status_code"]; exists {
					t.Errorf("local setup error invented an HTTP status: %#v", payload)
				}
			}
			if message != "Setup help takes no additional arguments." {
				t.Errorf("setup error message = %q", message)
			}
		})
	}
}

func TestMainAdminSetupCopiedVerificationCommand(t *testing.T) {
	work := filepath.Join(t.TempDir(), "Admin CLI with spaces & apostrophe's")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	binary, goBinary := "openai", filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		binary += ".exe"
		goBinary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	executable := filepath.Join(work, binary)
	build := exec.CommandContext(ctx, goBinary, "build", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	shells := []nativeShell{
		{"bash", "bash", "", "export OPENAI_ADMIN_KEY='synthetic-admin-setup-key'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "", "export OPENAI_ADMIN_KEY='synthetic-admin-setup-key'; ", []string{"-f", "-c"}},
	}
	if runtime.GOOS == "windows" {
		shells = []nativeShell{
			{"powershell", "powershell.exe", "", "$env:OPENAI_ADMIN_KEY = 'synthetic-admin-setup-key'; ", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
			{"pwsh", "pwsh.exe", "", "$env:OPENAI_ADMIN_KEY = 'synthetic-admin-setup-key'; ", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
		}
	}
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell %s unavailable: %v", shell.name, err)
				}
				t.Skipf("%s is not installed", shell.executable)
			}
			shell.executable = path
			invocation := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
			if shell.name == "powershell" || shell.name == "pwsh" {
				invocation = "& '" + strings.ReplaceAll(executable, "'", "''") + "'"
			}
			directory, home := t.TempDir(), t.TempDir()
			// No openai on PATH can accidentally satisfy the copied example.
			t.Setenv("PATH", directory)
			var unexpectedRequests atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				unexpectedRequests.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			t.Cleanup(proxy.Close)
			missing := runMainDispatchWithEnv(t, shell.name, adminCredentialsProxyEnv(proxy.URL, nil), executable,
				"--base-url", "https://api.openai.com/v1", "admin", "organization", "projects", "list")
			guideCommand := ""
			for line := range strings.SplitSeq(missing.stderr, "\n") {
				if strings.HasSuffix(line, " help setup admin") {
					guideCommand = strings.TrimSpace(line)
				}
			}
			if missing.code != 1 || missing.stdout != "" || guideCommand != invocation+" help setup admin" || unexpectedRequests.Load() != 0 {
				t.Fatalf("missing-key error lacks a runnable guide command: %+v; requests=%d", missing, unexpectedRequests.Load())
			}
			guide := runNativeShell(t, shell, directory, home, "not-a-request-url", guideCommand)
			var command string
			for line := range strings.SplitSeq(guide.stdout, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasSuffix(line, " admin organization projects list") {
					command = line
					break
				}
			}
			if guide.code != 0 || guide.stderr != "" || command != invocation+" --format text admin organization projects list" {
				t.Fatalf("guide lacks a runnable command for the invoked executable: %+v", guide)
			}
			var requests atomic.Int32
			var empty atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/organization/projects" {
					t.Errorf("verification request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-admin-setup-key" {
					t.Error("verification command did not use the synthetic admin environment key")
				}
				w.Header().Set("Content-Type", "application/json")
				if empty.Load() {
					io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
				} else {
					io.WriteString(w, `{"object":"list","data":[{"id":"proj_setup","object":"organization.project","name":"Synthetic setup project","created_at":1,"status":"active"}],"has_more":false}`)
				}
			}))
			t.Cleanup(server.Close)
			got := runNativeShell(t, shell, directory, home, server.URL, shell.setKey+command)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 || !strings.Contains(got.stdout, "proj_setup") {
				t.Fatalf("copied verification command failed: %+v, requests=%d", got, requests.Load())
			}
			if strings.Contains(got.stdout+got.stderr, "synthetic-admin-setup-key") {
				t.Error("verification output disclosed the synthetic admin key")
			}
			for _, field := range []string{"ID: proj_setup", "Name: Synthetic setup project"} {
				if !strings.Contains(got.stdout, field) {
					t.Errorf("verification output lacks the promised field %q: %q", field, got.stdout)
				}
			}
			empty.Store(true)
			got = runNativeShell(t, shell, directory, home, server.URL, shell.setKey+command)
			if got.code != 0 || got.stderr != "" || strings.TrimSpace(got.stdout) != "No results." || requests.Load() != 2 {
				t.Fatalf("empty verification does not match the guide: %+v; requests=%d", got, requests.Load())
			}
		})
	}
}
