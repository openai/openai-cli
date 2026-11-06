package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

type localRecoveryMode struct {
	name                string
	flags               []string
	structured, extract bool
}

func localRecoveryModes() []localRecoveryMode {
	return []localRecoveryMode{
		{name: "text", flags: []string{"--format-error", "text"}},
		{name: "JSON", flags: []string{"--format-error", "json"}, structured: true},
		{name: "extraction", flags: []string{"--format-error", "json", "--transform-error", "message"}, extract: true},
		{name: "quiet", flags: []string{"--format-error", "text", "--quiet"}},
	}
}

func localRecoveryEnvironment(t *testing.T) (string, []string, *atomic.Int32) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "synthetic-private-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	config := filepath.Join(home, "config")
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + config, "XDG_CONFIG_HOME=" + config,
		"ZDOTDIR=" + home, "OPENAI_API_KEY=sk-fake-local-recovery", "OPENAI_BASE_URL=" + server.URL, "NO_COLOR=1"}
	return home, env, &requests
}

func runLocalRecovery(t *testing.T, env []string, mode localRecoveryMode, args ...string) mainDispatchResult {
	t.Helper()
	argv := append([]string{"openai"}, mode.flags...)
	return runMainDispatchWithEnv(t, "", env, append(argv, args...)...)
}

func localRecoveryMessage(t *testing.T, got mainDispatchResult, mode localRecoveryMode, exit int) string {
	t.Helper()
	if got.code != exit || got.stdout != "" {
		t.Fatalf("local error changed status or stdout: %+v", got)
	}
	if strings.Contains(got.stderr, "synthetic-private") || strings.ContainsAny(got.stderr, "\x1b\a\r") {
		t.Fatalf("local error exposed a private value or terminal controls: %q", got.stderr)
	}
	if mode.extract {
		var message string
		if err := json.Unmarshal([]byte(got.stderr), &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	if mode.structured {
		value := decodeMainStructuredError(t, "json", got.stderr)
		if len(value) != 1 {
			t.Fatalf("local error invented fields: %#v", value)
		}
		return value["message"].(string)
	}
	return strings.TrimSpace(got.stderr)
}

func TestMainRecoveryRequiredValidationStages(t *testing.T) {
	for _, mode := range localRecoveryModes() {
		t.Run(mode.name, func(t *testing.T) {
			_, env, requests := localRecoveryEnvironment(t)
			for _, stage := range []struct {
				args []string
				flag string
			}{
				{[]string{"batches", "download"}, "--output"},
				{[]string{"batches", "download", "--output", "-"}, "--batch-id"},
			} {
				args := append([]string{"--project", "synthetic-private-project"}, stage.args...)
				failed := runLocalRecovery(t, env, mode, args...)
				message := localRecoveryMessage(t, failed, mode, 1)
				want := "Missing required options: " + stage.flag + ".\nOptions and examples: openai batches download --help"
				if message != want || requests.Load() != 0 {
					t.Fatalf("required input recovery: %q; want %q; requests=%d", message, want, requests.Load())
				}
				_, copied, found := strings.Cut(message, "Options and examples: ")
				if !found {
					t.Fatal("required input recovery omitted executable help")
				}
				help := runMainDispatchWithEnv(t, "", env, strings.Fields(copied)...)
				if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, "openai batches download") || requests.Load() != 0 {
					t.Fatalf("copied required input help failed: %+v; requests=%d", help, requests.Load())
				}
			}
		})
	}
}

func TestMainRecoveryManpageOutput(t *testing.T) {
	for _, stage := range []string{"prepare", "partial"} {
		for _, mode := range localRecoveryModes() {
			t.Run(stage+"/"+mode.name, func(t *testing.T) {
				home, env, requests := localRecoveryEnvironment(t)
				failedPath := filepath.Join(home, "synthetic-private-output")
				original := []byte("preserve this existing file\n")
				var preservedPath string
				if stage == "prepare" {
					preservedPath = failedPath
					if err := os.WriteFile(preservedPath, original, 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					blocked := filepath.Join(failedPath, "man1", "openai.1.gz")
					if err := os.MkdirAll(blocked, 0700); err != nil {
						t.Fatal(err)
					}
					preservedPath = filepath.Join(blocked, "existing")
					if err := os.WriteFile(preservedPath, original, 0600); err != nil {
						t.Fatal(err)
					}
				}
				failed := runLocalRecovery(t, env, mode, "@manpages", "--text", "--gzip", "--output", failedPath)
				message := localRecoveryMessage(t, failed, mode, 1)
				if !strings.Contains(message, "--output") || !strings.Contains(message, "permissions") {
					t.Fatalf("missing destination repair: %q", message)
				}
				var partial []byte
				if stage == "partial" {
					if !strings.Contains(message, "Some manpage files may be incomplete") {
						t.Fatalf("missing partial-state guidance: %q", message)
					}
					var err error
					partial, err = os.ReadFile(filepath.Join(failedPath, "man1", "openai.1"))
					if err != nil || len(partial) == 0 {
						t.Fatalf("completed text manpage was lost: %v", err)
					}
				} else if !strings.Contains(message, "Could not create the manpage folder") {
					t.Fatalf("wrong failed stage: %q", message)
				}
				// Follow --output guidance with a new isolated writable destination.
				recoveredPath := filepath.Join(home, "recovered")
				recovered := runLocalRecovery(t, env, mode, "@manpages", "--text", "--gzip", "--output", recoveredPath)
				if recovered.code != 0 || recovered.stdout != "" {
					t.Fatalf("manpage retry failed: %+v", recovered)
				}
				if mode.name != "text" && recovered.stderr != "" {
					t.Fatalf("save receipt polluted a suppressed stream: %+v", recovered)
				}
				text, err := os.ReadFile(filepath.Join(recoveredPath, "man1", "openai.1"))
				if err != nil || len(text) == 0 {
					t.Fatalf("missing recovered text manpage: %v", err)
				}
				compressed, err := os.ReadFile(filepath.Join(recoveredPath, "man1", "openai.1.gz"))
				if err != nil {
					t.Fatal(err)
				}
				reader, err := gzip.NewReader(bytes.NewReader(compressed))
				if err != nil {
					t.Fatal(err)
				}
				uncompressed, readErr := io.ReadAll(reader)
				closeErr := reader.Close()
				if readErr != nil || closeErr != nil || !bytes.Equal(text, uncompressed) {
					t.Fatalf("recovered manpage formats differ: %v, %v", readErr, closeErr)
				}
				kept, err := os.ReadFile(preservedPath)
				if err != nil || !bytes.Equal(original, kept) {
					t.Fatalf("retry changed an existing file: %v", err)
				}
				if partial != nil {
					kept, err = os.ReadFile(filepath.Join(failedPath, "man1", "openai.1"))
					if err != nil || !bytes.Equal(partial, kept) {
						t.Fatalf("retry changed prior partial output: %v", err)
					}
				}
				if requests.Load() != 0 {
					t.Fatalf("local recovery made %d requests", requests.Load())
				}
			})
		}
	}
}

func TestMainRecoveryManpageGzipDefault(t *testing.T) {
	for _, mode := range localRecoveryModes() {
		t.Run(mode.name, func(t *testing.T) {
			home, env, requests := localRecoveryEnvironment(t)
			destination := filepath.Join(home, "manpages")
			got := runLocalRecovery(t, env, mode, "@manpages", "--output", destination)
			if got.code != 0 || got.stdout != "" {
				t.Fatalf("default manpage output failed: %+v", got)
			}
			if mode.name != "text" && got.stderr != "" {
				t.Fatalf("default save receipt polluted a suppressed stream: %+v", got)
			}
			if _, err := os.Stat(filepath.Join(destination, "man1", "openai.1")); !os.IsNotExist(err) {
				t.Fatalf("default unexpectedly created an uncompressed manpage: %v", err)
			}
			compressed, err := os.ReadFile(filepath.Join(destination, "man1", "openai.1.gz"))
			if err != nil {
				t.Fatal(err)
			}
			reader, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || !bytes.Contains(content, []byte("openai")) {
				t.Fatalf("default gzip manpage is unusable: %v, %v", readErr, closeErr)
			}
			if requests.Load() != 0 {
				t.Fatalf("default manpage output made %d requests", requests.Load())
			}
		})
	}
}

func TestMainRecoveryCompletionSetup(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"orphan profile", "Use --install-picker or --uninstall-picker with setup options.", []string{"@completion", "bash", "--profile"}},
		{"conflicting actions", "Choose one setup action. Automatic setup does not accept a shell or profile override.", []string{"@completion", "bash", "--install-picker", "--uninstall-picker", "--profile"}},
		{"PowerShell", "PowerShell uses normal Tab completion.", []string{"@completion", "pwsh", "--install-picker", "--profile"}},
	} {
		for _, mode := range localRecoveryModes() {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				home, env, requests := localRecoveryEnvironment(t)
				profile := filepath.Join(home, "synthetic-private-profile")
				original := []byte("# existing startup content\n")
				if err := os.WriteFile(profile, original, 0600); err != nil {
					t.Fatal(err)
				}
				args := append(append([]string{}, tc.args...), profile)
				failed := runLocalRecovery(t, env, mode, args...)
				message := localRecoveryMessage(t, failed, mode, 2)
				if !strings.Contains(message, tc.want) {
					t.Fatalf("setup repair was lost: %q", message)
				}
				// Inspect the named option safely before choosing to change shell setup.
				helpArgs := []string{"@completion", "bash", "--install-picker", "--profile", profile, "--help"}
				wantHelp := "--install-picker"
				if tc.name == "PowerShell" {
					helpArgs = []string{"images", "generate", "--help"}
					wantHelp = "openai images generate"
				}
				help := runLocalRecovery(t, env, mode, helpArgs...)
				if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, wantHelp) {
					t.Fatalf("safe repair help failed: %+v", help)
				}
				if tc.name != "PowerShell" {
					if !strings.Contains(message, "Run openai help --all "+completionTestTopic()+" for setup options.") {
						t.Fatalf("setup diagnostic omitted the full reference command: %q", message)
					}
					if reference := runLocalRecovery(t, env, mode, "help", "--all", "@completion"); reference != help {
						t.Fatalf("printed repair command differs from the option-specific reference: %+v", reference)
					}
				}
				kept, err := os.ReadFile(profile)
				if err != nil || !bytes.Equal(kept, original) {
					t.Fatalf("validation/help changed the startup file: %v", err)
				}
				entries, err := os.ReadDir(home)
				if err != nil || len(entries) != 1 {
					t.Fatalf("validation/help wrote setup state: %v, %v", entries, err)
				}
				if requests.Load() != 0 {
					t.Fatalf("setup validation made %d requests", requests.Load())
				}
			})
		}
	}
}

func TestMainRecoveryCompletionLauncherControls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix launcher filenames and Bash")
	}
	work, binary := buildNativeShellCLI(t)
	decoy := t.TempDir()
	if err := os.WriteFile(filepath.Join(decoy, "openai"), []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", decoy+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_BASE_URL", "invalid")
	t.Setenv("OPENAI_CLI_NO_UPDATE_CHECK", "1")
	t.Setenv("NO_COLOR", "1")
	for _, name := range []string{"sample\u202eexe", "sample\nexe"} {
		launcher := filepath.Join(work, name)
		if err := os.Link(filepath.Join(work, binary), launcher); err != nil {
			t.Fatal(err)
		}
		args := []string{"--format-error", "json", "@completion", "bash", "--profile", "synthetic"}
		for _, bash := range []bool{false, true} {
			cmd := exec.CommandContext(t.Context(), launcher, args...)
			if bash {
				cmd = exec.CommandContext(t.Context(), "/bin/bash", append([]string{"-c", `"$@"; exit $?`, "bash", launcher}, args...)...)
			}
			raw, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
				t.Fatalf("expected setup error: %v, %q", err, raw)
			}
			var diagnostic struct{ Message string }
			if err := json.Unmarshal(raw, &diagnostic); err != nil {
				t.Fatal(err)
			}
			if strings.ContainsAny(diagnostic.Message, "\n\u202e") {
				t.Fatalf("unsafe launcher spelling in diagnostic: %q", diagnostic.Message)
			}
			if !bash {
				if !strings.Contains(diagnostic.Message, "Use the same executable with the arguments help, --all, and @completion") {
					t.Fatalf("unknown shell must receive argument-only guidance: %q", diagnostic.Message)
				}
				continue
			}
			_, suggestion, found := strings.Cut(diagnostic.Message, " Run ")
			suggestion, ok := strings.CutSuffix(suggestion, " for setup options.")
			if !found || !ok {
				t.Fatalf("missing recovery command: %q", diagnostic.Message)
			}
			help, err := exec.CommandContext(t.Context(), "/bin/bash", "-c", suggestion+"; exit $?").CombinedOutput()
			if err != nil || !bytes.Contains(help, []byte("\nFLAGS\n")) || bytes.Contains(help, []byte(name)) {
				t.Fatalf("recovery command used the wrong executable or exposed a control: %v, %q", err, help)
			}
		}
	}
}

func TestMainRecoveryInlinePreferencePrivacy(t *testing.T) {
	for _, setting := range []string{"on", "off"} {
		for _, mode := range localRecoveryModes() {
			t.Run(setting+"/"+mode.name, func(t *testing.T) {
				home, env, requests := localRecoveryEnvironment(t)
				config := filepath.Join(home, "config")
				if runtime.GOOS == "darwin" {
					config = filepath.Join(home, "Library", "Application Support")
				}
				path := filepath.Join(config, "openai", "image-preferences.json")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				original := []byte("synthetic-private-invalid-settings\n")
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{"images", "inline", setting}
				failed := runLocalRecovery(t, env, mode, args...)
				message := localRecoveryMessage(t, failed, mode, 1)
				location := "$XDG_CONFIG_HOME/openai/image-preferences.json, or $HOME/.config/openai/image-preferences.json when XDG_CONFIG_HOME is unset"
				switch runtime.GOOS {
				case "darwin":
					location = "$HOME/Library/Application Support/openai/image-preferences.json"
				case "windows":
					location = `%AppData%\openai\image-preferences.json`
				}
				for _, want := range []string{"Existing settings were kept", location, "Move an invalid settings file aside"} {
					if !strings.Contains(message, want) {
						t.Fatalf("preference recovery lost %q: %q", want, message)
					}
				}
				kept, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(original, kept) {
					t.Fatalf("failed preference update changed original: %v", err)
				}
				// Follow the message by preserving the invalid settings file before retrying.
				preserved := path + ".preserved"
				if err := os.Rename(path, preserved); err != nil {
					t.Fatal(err)
				}
				recovered := runLocalRecovery(t, env, mode, args...)
				if recovered.code != 0 || recovered.stdout != "" {
					t.Fatalf("preference recovery failed: %+v", recovered)
				}
				if mode.name != "text" && recovered.stderr != "" {
					t.Fatalf("receipt polluted a suppressed stream: %+v", recovered)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var value struct {
					Version int   `json:"version"`
					Inline  *bool `json:"inline"`
				}
				if err := json.Unmarshal(data, &value); err != nil || value.Version != 1 || value.Inline == nil || *value.Inline != (setting == "on") {
					t.Fatalf("wrong recovered preference: %s, %v", data, err)
				}
				kept, err = os.ReadFile(preserved)
				if err != nil || !bytes.Equal(original, kept) {
					t.Fatalf("recovery lost preserved settings: %v", err)
				}
				if requests.Load() != 0 {
					t.Fatalf("preference recovery made %d requests", requests.Load())
				}
			})
		}
	}
}
