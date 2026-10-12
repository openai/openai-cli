package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func pickerSetupProcessEnv(home string) []string {
	return []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "APPDATA=" + filepath.Join(home, "config"), "ZDOTDIR=" + home}
}

func pickerSetupProcessConfig(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support")
	}
	return filepath.Join(home, "config")
}

func TestMainPickerShellSetupRoundTrip(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := t.TempDir()
			profile := filepath.Join(home, "startup with spaces")
			original := "# personal startup\n"
			if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"--install-picker", "--install-picker", "--uninstall-picker", "--uninstall-picker"} {
				got := runMainDispatchWithEnv(t, shell, pickerSetupProcessEnv(home), "openai", "@completion", shell, action, "--profile", profile)
				if got.code != 0 || got.stdout != "" {
					t.Fatalf("setup failed: %+v", got)
				}
				data, err := os.ReadFile(profile)
				if err != nil {
					t.Fatal(err)
				}
				if action == "--install-picker" {
					if got.stderr != "Tab setup saved for future terminals. Existing custom bindings are preserved.\n" || !strings.HasPrefix(string(data), original) || strings.Count(string(data), "# >>> openai image picker") != 1 {
						t.Fatalf("installation lost content or duplicated setup: %q, %q", got.stderr, data)
					}
				} else if string(data) != original || got.stderr != "Tab shortcut setup removed. Open a new terminal to finish.\n" {
					t.Fatalf("removal lost original content: %q, %q", got.stderr, data)
				}
			}
		})
	}
}

func TestMainPickerShellSetupBashRemovalAfterLoginPrecedenceChanges(t *testing.T) {
	home := t.TempDir()
	env := pickerSetupProcessEnv(home)
	original := "# original login startup\n"
	preferred := "# newly preferred login startup\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "@completion", "bash", "--install-picker")
	if got.code != 0 || got.stdout != "" || got.stderr != "Tab setup saved for future terminals. Existing custom bindings are preserved.\n" {
		t.Fatalf("installation failed: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(preferred), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got = runMainDispatchWithEnv(t, "bash", env, "openai", "@completion", "bash", "--uninstall-picker")
		if got.code != 0 || got.stdout != "" || got.stderr != "Tab shortcut setup removed. Open a new terminal to finish.\n" {
			t.Fatalf("removal failed: %+v", got)
		}
		for name, want := range map[string]string{".profile": original, ".bash_profile": preferred} {
			data, err := os.ReadFile(filepath.Join(home, name))
			if err != nil || string(data) != want {
				t.Fatalf("removal did not restore %s: %q, %v", name, data, err)
			}
		}
		scripts, err := filepath.Glob(filepath.Join(pickerSetupProcessConfig(home), "openai", "shell", "picker-bash-*.bash"))
		if err != nil || len(scripts) != 0 {
			t.Fatalf("removal left obsolete scripts: %v, %v", scripts, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".bash_login")); !os.IsNotExist(err) {
			t.Fatalf("removal created an absent startup file: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
			t.Fatalf("removal left a created startup file: %v", err)
		}
	}
}

func TestMainPickerShellSetupBashRemovalRestoresAbsentLoginProfile(t *testing.T) {
	home := t.TempDir()
	env := pickerSetupProcessEnv(home)
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "@completion", "bash", "--install-picker")
	if got.code != 0 || got.stdout != "" || got.stderr != "Tab setup saved for future terminals. Existing custom bindings are preserved.\n" {
		t.Fatalf("installation failed: %+v", got)
	}
	personal := "# later login settings\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(personal), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got = runMainDispatchWithEnv(t, "bash", env, "openai", "@completion", "bash", "--uninstall-picker")
		if got.code != 0 || got.stdout != "" || got.stderr != "Tab shortcut setup removed. Open a new terminal to finish.\n" {
			t.Fatalf("removal failed: %+v", got)
		}
		for _, name := range []string{".bash_profile", ".bashrc"} {
			if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
				t.Fatalf("removal left a newly created startup file %s: %v", name, err)
			}
		}
		data, err := os.ReadFile(filepath.Join(home, ".profile"))
		if err != nil || string(data) != personal {
			t.Fatalf("removal changed later personal login settings: %q, %v", data, err)
		}
	}
}

func TestMainPickerShellSetupPowerShellFallback(t *testing.T) {
	const guidance = "PowerShell uses normal Tab completion. Type openai images generate and press Enter to open the image picker."
	for _, action := range []string{"--install-picker", "--uninstall-picker"} {
		for _, format := range []string{"text", "json"} {
			t.Run(action+"/"+format, func(t *testing.T) {
				home := t.TempDir()
				got := runMainDispatchWithEnv(t, "pwsh", pickerSetupProcessEnv(home), "openai", "--format-error", format, "@completion", "pwsh", action)
				if got.code != 2 || got.stdout != "" {
					t.Fatalf("unexpected fallback result: %+v", got)
				}
				if format == "json" {
					var value struct {
						Message string `json:"message"`
					}
					if err := json.Unmarshal([]byte(got.stderr), &value); err != nil || value.Message != guidance {
						t.Fatalf("structured guidance lost: %v, %q", err, got.stderr)
					}
				} else if strings.TrimSpace(got.stderr) != guidance {
					t.Fatalf("guidance lost at process stderr: %q", got.stderr)
				}
				entries, err := os.ReadDir(home)
				if err != nil || len(entries) != 0 {
					t.Fatalf("unsupported setup wrote files: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestMainPickerShellSetupPreservesModifiedProfileAndPrivateErrors(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			home := t.TempDir()
			profile := filepath.Join(home, "private startup")
			original := "# >>> openai image picker modified\n# personal content\n"
			if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			got := runMainDispatchWithEnv(t, "zsh", pickerSetupProcessEnv(home), "openai", "--format-error", format, "@completion", "zsh", "--install-picker", "--profile", profile)
			if got.code == 0 || got.stdout != "" || strings.Contains(got.stderr, home) {
				t.Fatalf("failed setup lost its error or leaked a private path: %+v", got)
			}
			message := got.stderr
			if format == "json" {
				var value struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal([]byte(got.stderr), &value); err != nil {
					t.Fatalf("error stream is not one JSON document: %v, %q", err, got.stderr)
				}
				message = value.Message
			}
			if !strings.Contains(message, "Could not finish Tab shortcut setup") {
				t.Fatalf("recovery guidance lost: %q", got.stderr)
			}
			data, err := os.ReadFile(profile)
			if err != nil || string(data) != original {
				t.Fatalf("modified startup changed: %q, %v", data, err)
			}
		})
	}
}

func TestMainPickerShellSetupRefusesUnsafeConfigurationAncestry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission fixtures; Windows has native ACL coverage")
	}
	for _, unsafeLevel := range []string{"configuration", "ancestor"} {
		for _, format := range []string{"text", "json"} {
			t.Run(unsafeLevel+"/"+format, func(t *testing.T) {
				home := t.TempDir()
				parent := filepath.Join(home, "settings")
				configuration := filepath.Join(parent, "config")
				if runtime.GOOS == "darwin" {
					configuration = pickerSetupProcessConfig(home)
					parent = filepath.Dir(configuration)
				}
				if err := os.MkdirAll(configuration, 0700); err != nil {
					t.Fatal(err)
				}
				unsafe := configuration
				if unsafeLevel == "ancestor" {
					unsafe = parent
				}
				if err := os.Chmod(unsafe, 0770); err != nil {
					t.Fatal(err)
				}
				profile := filepath.Join(home, "startup")
				original := "# personal settings\n"
				if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				env := append(pickerSetupProcessEnv(home), "XDG_CONFIG_HOME="+configuration)
				got := runMainDispatchWithEnv(t, "zsh", env, "openai", "--format-error", format, "@completion", "zsh", "--install-picker", "--profile", profile)
				if got.code == 0 || got.stdout != "" || got.stderr == "" || strings.Contains(got.stderr, home) {
					t.Fatalf("unsafe setup did not fail privately: %+v", got)
				}
				if format == "json" && !json.Valid([]byte(got.stderr)) {
					t.Fatalf("error is not one JSON document: %q", got.stderr)
				}
				data, err := os.ReadFile(profile)
				if err != nil || string(data) != original {
					t.Fatalf("unsafe setup changed profile: %q, %v", data, err)
				}
				entries, err := os.ReadDir(configuration)
				if err != nil || len(entries) != 0 {
					t.Fatalf("unsafe setup created state: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestMainPickerShellSetupReadOnlyModesDoNotWrite(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"images", "generate", "--help"}, {"--format", "json", "images", "generate", "--help"},
		{"@completion", "zsh"}, {"@completion", "zsh", "--picker"},
		{"__complete", "images", ""}, {"@completion", "--install-picker", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			got := runMainDispatchWithEnv(t, "zsh", pickerSetupProcessEnv(home), append([]string{"openai"}, args...)...)
			if got.code != 0 || got.stderr != "" || got.stdout == "" {
				t.Fatalf("read-only command failed: %+v", got)
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("read-only command wrote state: %v, %v", entries, err)
			}
		})
	}
}

func TestMainPickerShellSetupPartialRemovalReportsFailureAfterCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink fixture")
	}
	for _, format := range []string{"auto", "json"} {
		t.Run(format, func(t *testing.T) {
			home := t.TempDir()
			env := pickerSetupProcessEnv(home)
			profile := filepath.Join(home, ".profile")
			original := "# personal login settings\n"
			if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			got := runMainDispatchWithEnv(t, "bash", env, "openai", "@completion", "bash", "--install-picker")
			if got.code != 0 || got.stdout != "" || got.stderr != "Tab setup saved for future terminals. Existing custom bindings are preserved.\n" {
				t.Fatalf("installation failed: %+v", got)
			}
			if err := os.Symlink(profile, filepath.Join(home, ".bash_profile")); err != nil {
				t.Fatal(err)
			}
			got = runMainDispatchWithEnv(t, "bash", env, "openai", "--format-error", format, "@completion", "bash", "--uninstall-picker")
			if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "Could not finish Tab shortcut setup") || strings.Contains(got.stderr, home) {
				t.Fatalf("partial removal lost a safe failure diagnostic: %+v", got)
			}
			if format == "json" && !json.Valid([]byte(got.stderr)) {
				t.Fatalf("partial failure is not one JSON document: %q", got.stderr)
			}
			data, err := os.ReadFile(profile)
			if err != nil || string(data) != original {
				t.Fatalf("failure prevented independent cleanup: %q, %v", data, err)
			}
			if info, err := os.Lstat(filepath.Join(home, ".bash_profile")); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("removal changed the unrelated symlink: %v, %v", info, err)
			}
			scripts, err := filepath.Glob(filepath.Join(pickerSetupProcessConfig(home), "openai", "shell", "picker-bash-*.bash"))
			if err != nil || len(scripts) != 0 {
				t.Fatalf("partial removal left owned scripts: %v, %v", scripts, err)
			}
		})
	}
}

func TestMainPickerShellSetupIgnoresRequestConfiguration(t *testing.T) {
	for _, configuration := range []string{"invalid base URL", "missing mTLS files"} {
		for _, spelling := range []struct {
			name, install, remove, profile string
		}{
			{"double dash", "--install-picker=true", "--uninstall-picker", "--profile"},
			{"single dash", "-install-picker", "-uninstall-picker=true", "-profile"},
			{"empty bool value", "-install-picker=", "--uninstall-picker=", "-profile"},
			{"padded flag", " -install-picker ", " --uninstall-picker ", " -profile "},
		} {
			t.Run(configuration+"/"+spelling.name, func(t *testing.T) {
				home := t.TempDir()
				profile := filepath.Join(home, "startup")
				env := pickerSetupProcessEnv(home)
				if configuration == "invalid base URL" {
					env = append(env, "OPENAI_BASE_URL=invalid")
				} else {
					env = append(env, "OPENAI_BASE_URL=https://api.example.invalid",
						"OPENAI_MTLS_CLIENT_CERT_FILE="+filepath.Join(home, "absent-cert"),
						"OPENAI_MTLS_CLIENT_KEY_FILE="+filepath.Join(home, "absent-key"))
				}
				for _, action := range []string{spelling.install, spelling.remove} {
					got := runMainDispatchWithEnv(t, "zsh", env, "openai", "--format-error", "json", "@completion", "zsh", action, spelling.profile, profile)
					if got.code != 0 || got.stderr != "" || got.stdout != "" {
						t.Fatalf("local setup depended on request configuration: %+v", got)
					}
					if action == spelling.install {
						data, err := os.ReadFile(profile)
						if err != nil || strings.Count(string(data), "# >>> openai image picker") != 1 {
							t.Fatalf("silent installation did not create setup: %q, %v", data, err)
						}
					}
				}
				if _, err := os.Lstat(profile); !os.IsNotExist(err) {
					t.Fatalf("setup round trip did not restore absent profile: %v", err)
				}
			})
		}
	}
}

func TestMainPickerShellSetupDoesNotExemptAPIArguments(t *testing.T) {
	for _, args := range [][]string{
		{"images", "generate", "--prompt", "--install-picker"},
		{"images", "generate", "--prompt", "@completion --uninstall-picker"},
		{"--project", "@completion", "images", "generate", "--prompt", "--install-picker"},
		{"--project=@completion", "images", "generate", "--prompt", "--install-picker"},
		{"--project", "__complete", "images", "generate", "--prompt", "--install-picker"},
		{"images", "generate", "--prompt", "__complete --install-picker"},
		{"models", "retrieve", "--model", "@completion"},
		{"models", "retrieve", "--model=__complete"},
		{"models", "list", "--", "@completion", "--install-picker"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			env := append(pickerSetupProcessEnv(home), "OPENAI_BASE_URL=invalid")
			got := runMainDispatchWithEnv(t, "zsh", env, append([]string{"openai"}, args...)...)
			if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "OPENAI_BASE_URL must start") {
				t.Fatalf("non-setup arguments bypassed request validation: %+v", got)
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("non-setup arguments wrote files: %v, %v", entries, err)
			}
		})
	}
}

func TestMainCompletionArgumentsIgnoreRequestConfiguration(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	missing := filepath.Join(t.TempDir(), "missing.pem")
	run := func(t *testing.T, args, configuration []string) mainDispatchResult {
		t.Helper()
		home := t.TempDir()
		env := append(pickerSetupProcessEnv(home), "OPENAI_BASE_URL="+server.URL)
		env = append(env, configuration...)
		got := runMainDispatchWithEnv(t, "zsh", env, append([]string{"openai", "@completion", "zsh"}, args...)...)
		entries, err := os.ReadDir(home)
		if err != nil || len(entries) != 0 {
			t.Fatalf("local completion wrote files: %v, %v", entries, err)
		}
		if len(requests) != 0 {
			t.Fatalf("local completion made %d requests", len(requests))
		}
		return got
	}
	script := run(t, nil, nil)
	if script.code != 0 || script.stderr != "" || !strings.Contains(script.stdout, "__openai_zsh_autocomplete") {
		t.Fatalf("completion script control failed: %+v", script)
	}
	for _, test := range []struct {
		args     []string
		wantCode int
	}{
		{[]string{"--install-picker=false"}, 0},
		{[]string{"-install-picker=false"}, 0},
		{[]string{"-install-picker", "-install-picker=false"}, 0},
		{[]string{"--install-picker", "-install-picker=false"}, 0},
		{[]string{"--uninstall-picker", "-uninstall-picker=false"}, 0},
		{[]string{"-profile", "-install-picker"}, 2},
		{[]string{"-profile", "--install-picker"}, 2},
		{[]string{"-profile=-install-picker"}, 2},
		{[]string{"--", "-install-picker"}, 0},
		{[]string{" -- ", "-install-picker"}, 0},
		{[]string{"-", "--install-picker"}, 0},
		{[]string{"-1", "--install-picker"}, 0},
		{[]string{"-@", "--install-picker"}, 0},
		{[]string{"--profile", "--install-picker"}, 2},
		{[]string{"--", "--install-picker"}, 0},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			want := run(t, test.args, nil)
			if want.code != test.wantCode {
				t.Fatalf("clean configuration returned exit %d, want %d: %+v", want.code, test.wantCode, want)
			}
			if test.wantCode == 0 && want != script {
				t.Fatalf("non-setup arguments changed the completion script: %+v", want)
			}
			if test.wantCode != 0 && (want.stdout != "" || !strings.Contains(want.stderr, "Check your arguments with --help.")) {
				t.Fatalf("invalid profile arguments lost their local diagnostic: %+v", want)
			}
			for _, configuration := range []struct {
				name string
				env  []string
			}{
				{"invalid URL", []string{"OPENAI_BASE_URL=invalid"}},
				{"missing mTLS files", []string{"OPENAI_MTLS_CLIENT_CERT_FILE=" + missing, "OPENAI_MTLS_CLIENT_KEY_FILE=" + missing}},
			} {
				t.Run(configuration.name, func(t *testing.T) {
					if got := run(t, test.args, configuration.env); got != want {
						t.Fatalf("request configuration changed local completion: got %+v; want %+v", got, want)
					}
				})
			}
		})
	}
}
