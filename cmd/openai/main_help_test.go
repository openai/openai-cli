package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/urfave/cli/v3"
)

func TestMainHelpWelcome(t *testing.T) {
	want := runMainDispatch(t, "bash", "openai")
	if want.code != 0 || want.stderr != "" {
		t.Fatalf("welcome failed: %+v", want)
	}
	if lines := len(strings.Split(strings.TrimSpace(want.stdout), "\n")); lines > 20 {
		t.Errorf("welcome uses %d lines; want at most 20", lines)
	}
	for _, text := range []string{"openai help setup", "openai models list", "--help", "help --all", "openai images --help", "openai help --all images generate"} {
		if !strings.Contains(want.stdout, text) {
			t.Errorf("welcome lacks %q: %s", text, want.stdout)
		}
	}
	for _, args := range [][]string{
		{"openai", "help"}, {"openai", "--help"}, {"openai", "-h"}, {"openai", "--h"},
		{"openai", "--debug", "help"}, {"openai", "--organization", "help", "--help"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			if got := runMainDispatch(t, "bash", args...); got != want {
				t.Errorf("got %+v; want %+v", got, want)
			}
		})
	}
}

func TestMainHelpCommandRoutes(t *testing.T) {
	for _, path := range [][]string{{"images"}, {"images", "generate"}, {"models", "retrieve"}, {"responses", "create"}} {
		t.Run(strings.Join(path, "/"), func(t *testing.T) {
			control := append([]string{"openai"}, path...)
			want := runMainDispatch(t, "bash", append(control, "--help")...)
			if want.code != 0 || want.stderr != "" || want.stdout == "" {
				t.Fatalf("command help failed: %+v", want)
			}
			if lines := len(strings.Split(strings.TrimSpace(want.stdout), "\n")); lines > 30 {
				t.Errorf("short help uses %d lines; want at most 30", lines)
			}
			if !strings.Contains(want.stdout, "help --all "+strings.Join(path, " ")) {
				t.Errorf("short help lacks the full reference command: %s", want.stdout)
			}
			routes := [][]string{
				append([]string{"openai", "help"}, path...),
				append(append([]string{"openai"}, path...), "-h"),
				append(append([]string{"openai"}, path...), "--h"),
				append(append([]string{"openai", "help"}, path...), "--h"),
				append([]string{"openai", "--debug", "help"}, path...),
				append([]string{"openai", "--base-url", "http://127.0.0.1:1", "help"}, path...),
			}
			if len(path) > 1 {
				routes = append(routes, append([]string{"openai", path[0], "help"}, path[1:]...))
				routes = append(routes, append([]string{"openai", path[0], "--debug", "help"}, path[1:]...))
				routes = append(routes, append([]string{"openai", path[0], "--transform", "help", "help"}, path[1:]...))
				routes = append(routes, append([]string{"openai", path[0], "help", "--"}, path[1:]...))
			}
			for _, args := range routes {
				if got := runMainDispatch(t, "bash", args...); got != want {
					t.Errorf("args %q: got %+v; want %+v", args, got, want)
				}
			}
		})
	}
}

// Check the public executable rather than only template configuration: the
// generated command tree remains the authority for each available option.
func TestMainHelpFullReferencePreservesEveryFlag(t *testing.T) {
	var visit func(*cli.Command, []string, []cli.Flag)
	visit = func(command *cli.Command, path []string, inherited []cli.Flag) {
		if command.Hidden {
			return
		}
		flags := append(slices.Clone(inherited), command.VisibleFlags()...)
		t.Run(strings.Join(append([]string{"openai"}, path...), "/"), func(t *testing.T) {
			got := runMainDispatch(t, "bash", append([]string{"openai", "help", "--all"}, path...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("full help failed: %+v", got)
			}
			for _, flag := range flags {
				for _, name := range flag.Names() {
					prefix := "--"
					if len(name) == 1 {
						prefix = "-"
					}
					if !strings.Contains(got.stdout, prefix+name) {
						t.Errorf("full help lost flag %s%s", prefix, name)
					}
				}
			}
			for _, child := range command.VisibleCommands() {
				if !strings.Contains(got.stdout, child.Name) {
					t.Errorf("full help lost command %q", child.Name)
				}
			}
			for _, text := range []string{"--header", "OPENAI_CUSTOM_HEADERS"} {
				if !strings.Contains(got.stdout, text) {
					t.Errorf("full help lost request-header documentation %q", text)
				}
			}
			var required []string
			for _, flag := range command.VisibleFlags() {
				ordinary, isOrdinary := flag.(cli.RequiredFlag)
				body, isBody := flag.(requestflag.RequiredFlagOrStdin)
				if (isOrdinary && ordinary.IsRequired()) || (isBody && body.IsRequiredAsFlagOrStdin()) {
					required = append(required, flag.Names()[0])
				}
			}
			if len(required) > 0 {
				short := runMainDispatch(t, "bash", append([]string{"openai", "help"}, path...)...)
				if short.code != 0 || short.stderr != "" {
					t.Fatalf("short help failed: %+v", short)
				}
				for _, name := range required {
					if !strings.Contains(short.stdout, "--"+name) {
						t.Errorf("short help hid required option --%s", name)
					}
				}
			}
		})
		var persistent []cli.Flag
		for _, flag := range flags {
			if local, ok := flag.(cli.LocalFlag); ok && !local.IsLocal() {
				persistent = append(persistent, flag)
			}
		}
		for _, child := range command.Commands {
			visit(child, append(slices.Clone(path), child.Name), persistent)
		}
	}
	visit(cmd.Command, nil, nil)
}

func TestMainHelpFullReferenceRoutes(t *testing.T) {
	// Full reference routes must also agree on default values, not just names.
	var reference string
	for _, args := range [][]string{
		{"openai", "help", "--all", "images", "generate"},
		{"openai", "help", "images", "generate", "--all"},
		{"openai", "images", "help", "--all", "generate"},
		{"openai", "--debug", "help", "--all", "images", "generate"},
		{"openai", "images", "--debug", "help", "--all", "generate"},
		{"openai", "images", "help", "--all", "--", "generate"},
	} {
		got := runMainDispatch(t, "bash", args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("args %q: full reference failed: %+v", args, got)
		}
		if reference == "" {
			reference = got.stdout
		} else if got.stdout != reference {
			t.Errorf("args %q: full help differs between routes", args)
		}
		for _, text := range []string{"--prompt string", "-n int", "(default: auto)", "(default: 1)", "(default: png)", "(default: 100)", "(default: 0)"} {
			if !strings.Contains(got.stdout, text) {
				t.Errorf("args %q: full help lost label/default %q", args, text)
			}
		}
		for _, text := range []string{"--prompt dall-e-2", "-n dall-e-3", "--size gpt-image-2"} {
			if strings.Contains(got.stdout, text) {
				t.Errorf("args %q: misleading value label %q", args, text)
			}
		}
		for _, flag := range cmd.Command.Command("images").Command("generate").VisibleFlags() {
			name := flag.Names()[0]
			if !strings.Contains(got.stdout, "-"+name) {
				t.Errorf("args %q: full reference lost option %q", args, name)
			}
		}
		for _, text := range []string{"32000 characters", "60 minutes", "base64-encoded images", "divisible by 16"} {
			if !strings.Contains(strings.Join(strings.Fields(got.stdout), " "), text) {
				t.Errorf("args %q: full reference lost detail %q", args, text)
			}
		}
	}
}

func TestMainHelpFullReferenceNullableDefaults(t *testing.T) {
	for _, tc := range []struct {
		path              []string
		flag, wantDefault string
	}{
		{[]string{"beta:assistants", "create"}, "--description string", ""},
		{[]string{"images", "generate"}, "--output-format string", "(default: png)"},
	} {
		t.Run(strings.Join(tc.path, "/"), func(t *testing.T) {
			got := runMainDispatch(t, "bash", append([]string{"openai", "help", "--all"}, tc.path...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("full reference failed: %+v", got)
			}
			_, details, found := strings.Cut(got.stdout, tc.flag)
			if !found {
				t.Fatalf("full reference lost %s", tc.flag)
			}
			// Keep wrapped prose and defaults, stopping at the next flag.
			details, _, _ = strings.Cut(details, "\n   --")
			details = strings.Join(strings.Fields(details), " ")
			if tc.wantDefault == "" {
				if strings.Contains(details, "(default:") {
					t.Errorf("unset nullable flag advertises a default: %s%s", tc.flag, details)
				}
			} else if !strings.Contains(details, tc.wantDefault) {
				t.Errorf("flag lost %q: %s%s", tc.wantDefault, tc.flag, details)
			}
		})
	}
}

func TestMainHelpFullReferenceDoesNotPrintCredentials(t *testing.T) {
	env := []string{
		"OPENAI_API_KEY=fake-env-api-key", "OPENAI_ADMIN_KEY=fake-env-admin-key",
		"OPENAI_WEBHOOK_SECRET=fake-env-webhook-secret",
	}
	for _, prefix := range [][]string{
		{"openai"},
		{"openai", "--api-key", "fake-argument-api-key", "--admin-api-key", "fake-argument-admin-key", "--webhook-secret", "fake-argument-webhook-secret"},
	} {
		for _, route := range [][]string{
			{"help", "--all"}, {"help", "--all", "images", "generate"},
			{"images", "help", "--all", "generate"},
		} {
			args := append(slices.Clone(prefix), route...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || got.stdout == "" {
				t.Errorf("reference failed: args %q, %+v", args, got)
			}
			for _, marker := range []string{"fake-env-", "fake-argument-"} {
				if strings.Contains(got.stdout+got.stderr, marker) {
					t.Errorf("full reference exposed %q credentials", marker)
				}
			}
		}
	}
}

func TestMainHelpDoesNotAdvertiseUnshippedFeatures(t *testing.T) {
	for _, args := range [][]string{
		{"openai"}, {"openai", "help", "setup"}, {"openai", "images", "--help"},
		{"openai", "images", "generate", "--help"},
	} {
		got := runMainDispatch(t, "bash", args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("help failed: %+v", got)
		}
		for _, text := range []string{"~/Downloads", "images preview", "images inline", "images options", "images models", "--output-dir", "--name", "--count", "--inline", "automatically saves", "readable output"} {
			if strings.Contains(got.stdout, text) {
				t.Errorf("args %q advertise unavailable feature %q", args, text)
			}
		}
	}
	imageHelp := runMainDispatch(t, "bash", "openai", "images", "generate", "--help")
	for _, text := range []string{"--model", "--prompt", "JSON"} {
		if !strings.Contains(imageHelp.stdout, text) {
			t.Errorf("image help does not explain the existing command's %q: %s", text, imageHelp.stdout)
		}
	}
}

func TestMainHelpIgnoresBrokenRequestConfiguration(t *testing.T) {
	env := []string{"OPENAI_BASE_URL=not-a-request-url", "OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem", "OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem"}
	for _, args := range [][]string{
		{"openai"}, {"openai", "help"}, {"openai", "--help"}, {"openai", "-h"},
		{"openai", "help", "setup"}, {"openai", "help", "--all"},
		{"openai", "images", "generate", "--help"},
		{"openai", "images", "generate", "--prompt", "help", "--help"},
		{"openai", "--debug", "help", "--all", "images", "generate"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || got.stdout == "" {
				t.Errorf("help depends on request configuration: %+v", got)
			}
		})
	}
}

func TestMainHelpSetupIsReadOnly(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "help must not make requests", http.StatusBadRequest)
	}))
	defer server.Close()
	home := t.TempDir()
	sentinel := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(sentinel, []byte("# existing shell settings\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=fake-key-must-not-be-printed"}
	for _, args := range [][]string{
		{"openai", "help", "setup"}, {"openai", "help", "setup", "--help"},
		{"openai", "--debug", "help", "setup"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("setup guide failed: %+v", got)
		}
		for _, text := range []string{"https://platform.openai.com/", "/api-keys", "read -rs OPENAI_API_KEY", "export OPENAI_API_KEY", "-AsSecureString", "PowerShell"} {
			if !strings.Contains(got.stdout, text) {
				t.Errorf("setup guide lacks %q", text)
			}
		}
		if !strings.Contains(strings.ToLower(got.stdout), "session") || !strings.Contains(strings.ToLower(got.stdout), "again") {
			t.Error("setup guide must explain entering the key again for a new terminal session")
		}
		if strings.Contains(got.stdout+got.stderr, "fake-key-must-not-be-printed") {
			t.Error("setup printed the current environment credential")
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("setup made %d network requests", requests.Load())
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 {
		t.Fatalf("setup changed the home directory: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "# existing shell settings\n" {
		t.Fatalf("setup changed shell configuration: %q, %v", data, err)
	}
}

func TestMainHelpWordsRemainRequestValues(t *testing.T) {
	for _, value := range []string{"help", "--help", "-h", "--h", "--all", "setup"} {
		for _, equals := range []bool{false, true} {
			t.Run(value+"/"+map[bool]string{false: "separate", true: "equals"}[equals], func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/images/generations" {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["prompt"] != value {
						t.Errorf("prompt was changed: %v, %v", body, err)
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"created":1,"data":[]}`)
				}))
				defer server.Close()
				args := []string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "images", "generate", "--model", "gpt-image-1"}
				if equals {
					args = append(args, "--prompt="+value)
				} else {
					args = append(args, "--prompt", value)
				}
				got := runMainDispatch(t, "bash", args...)
				if got.code != 0 || requests.Load() != 1 {
					t.Errorf("literal prompt was treated as help: %+v, requests=%d", got, requests.Load())
				}
			})
		}
	}
}

func TestMainHelpDoesNotBypassRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		env        []string
	}{
		{"base URL", "OPENAI_BASE_URL", []string{"OPENAI_BASE_URL=not-a-request-url"}},
		{"mTLS files", "mTLS", []string{"OPENAI_BASE_URL=https://127.0.0.1:1", "OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem", "OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem"}},
	} {
		for _, value := range []string{"help", "--help", "-h"} {
			t.Run(tc.name+"/"+value, func(t *testing.T) {
				got := runMainDispatchWithEnv(t, "bash", tc.env, "openai", "models", "retrieve", "--model", value)
				if got.code == 0 || !strings.Contains(got.stderr, tc.want) {
					t.Errorf("request configuration was bypassed: %+v", got)
				}
			})
		}
	}
}

func TestMainHelpRejectsUnknownTopics(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "help", "imaginary"}, {"openai", "help", "images", "imaginary"},
		{"openai", "images", "help", "imaginary"}, {"openai", "help", "--all", "images", "imaginary"},
		{"openai", "help", "setup", "unexpected"},
		{"openai", "images", "help", "--", "--help"},
		{"openai", "images", "help", "--", "--all"},
	} {
		got := runMainDispatch(t, "bash", args...)
		if got.code == 0 || got.stdout != "" || got.stderr == "" {
			t.Errorf("unknown help topic must fail with an error: args %q, %+v", args, got)
		}
	}
}
