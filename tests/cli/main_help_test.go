package cli_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainHelpWelcome(t *testing.T) {
	for _, args := range [][]string{
		{"./openai"}, {"./openai", "help"}, {"./openai", "--help"}, {"./openai", "-h"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatch(t, "bash", args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("welcome = %+v", got)
			}
			if lines := strings.Count(got.stdout, "\n"); lines > 24 {
				t.Errorf("welcome has %d lines; want at most 24", lines)
			}
			for _, want := range []string{"./openai help setup", "./openai images generate --prompt", "~/Downloads/gpt-images/", "./openai images generate --help", "help --all"} {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("welcome missing %q: %s", want, got.stdout)
				}
			}
			for _, unwanted := range []string{"--mtls-client", "--transform-error", "--base-url"} {
				if strings.Contains(got.stdout, unwanted) {
					t.Errorf("advanced option %q leaked into welcome", unwanted)
				}
			}
		})
	}
}

func TestMainHelpNestedCommandAndFullReference(t *testing.T) {
	control := runMainDispatch(t, "bash", "./openai", "images", "generate", "--help")
	if control.code != 0 || control.stderr != "" {
		t.Fatalf("command help = %+v", control)
	}
	// Everyday folder/model overrides should be discoverable on the first screen.
	if lines := strings.Count(control.stdout, "\n"); lines > 20 {
		t.Errorf("image quick help has %d lines; want at most 20", lines)
	}
	for _, want := range []string{"~/Downloads/gpt-images/", "--name robot", "--open", "--inline off", "./openai help setup", "./openai images generate --prompt", "./openai help --all images generate", "1 PNG", "automatic size and quality", "--model gpt-image-2.5-flare", `--output-dir "~/Downloads"`, "robot.png (existing files kept)", "--format json", "redirected output"} {
		if !strings.Contains(control.stdout, want) {
			t.Errorf("image quick help missing %q: %s", want, control.stdout)
		}
	}
	for _, unwanted := range []string{"--mtls-client", "--transform-error", "--partial-images", "--quality", "--output-format"} {
		if strings.Contains(control.stdout, unwanted) {
			t.Errorf("advanced option %q leaked into quick image help", unwanted)
		}
	}
	for _, args := range [][]string{
		{"./openai", "help", "images", "generate"},
		{"./openai", "images", "help", "generate"},
		{"./openai", "images", "generate", "-h"},
	} {
		if got := runMainDispatch(t, "bash", args...); got != control {
			t.Errorf("args %q = %+v; want %+v", args, got, control)
		}
	}
	full := runMainDispatch(t, "bash", "./openai", "help", "--all", "images", "generate")
	if full.code != 0 || full.stderr != "" {
		t.Fatalf("full help = %+v", full)
	}
	// Verify model limits and the output contract survive the full rendered
	// reference, not just the raw flags. Ignore the help renderer's line wraps.
	normalized := strings.Join(strings.Fields(full.stdout), " ")
	for _, want := range []string{
		"--prompt", "--output-dir", "--partial-images", "--mtls-client-cert-file", "--header", "OPENAI_CUSTOM_HEADERS",
		"--prompt string", "--size string", "-n int", "--output-compression int", "--stream bool",
		"--header string, -H string [ --header string, -H string ]",
		"32000 characters", "xhigh", "max", "divisible by 16", "60 minutes",
		"final image may be sent before", "CLI saving preset:", "API behavior:",
		"Piped or redirected output still saves images and prints readable paths.", "Explicit models and API output use API defaults",
		"images inline off (or on)", "images inline setup", "without an API call", "Existing files are never overwritten",
		"./openai images generate --prompt", "./openai images preview --open FILE", "./openai --format json images generate",
		".png, .jpg, .jpeg or .webp is optional", "actual format chooses the extension",
	} {
		if !strings.Contains(normalized, want) {
			t.Errorf("full help missing %q: %s", want, full.stdout)
		}
	}
	for _, line := range strings.Split(full.stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "openai ") && strings.Contains(line, "--prompt") {
			t.Errorf("full help example lost the runnable ./openai invocation: %s", line)
		}
	}
	nested := runMainDispatch(t, "bash", "./openai", "images", "help", "--all", "generate")
	if nested != full {
		t.Errorf("nested full help differs from root full help: %+v", nested)
	}
}

func TestMainHelpImageUsageErrorsAreConcise(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"typo", []string{"--prmpt", "synthetic-private-prompt"}, []string{`Unknown option "--prmpt".`, `Did you mean "--prompt"?`}},
		{"missing prompt value", []string{"--prompt"}, []string{`Option "--prompt" needs a value.`, `./openai images generate --prompt "A tiny orange robot"`}},
		{"missing model value", []string{"--model"}, []string{`Option "--model" needs a value.`}},
		{"missing folder value", []string{"--output-dir"}, []string{`Option "--output-dir" needs a value.`}},
		{"invalid count", []string{"-n", "not-a-number"}, []string{"Could not read the command options", "-n"}},
		{"hostile unknown option", []string{"--prmpt\x1b]0;untrusted-title\a"}, []string{"Unknown option", `\x1b`, `\a`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"./openai", "images", "generate"}, test.args...)
			got := runMainDispatch(t, "bash", args...)
			if got.code != 1 || got.stdout != "" {
				t.Fatalf("usage error changed exit status or printed help to stdout: %+v", got)
			}
			for _, want := range append(test.want, "Help: ./openai images generate --help") {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("usage error missing %q: %q", want, got.stderr)
				}
			}
			if lines := strings.Count(got.stderr, "\n"); lines > 4 {
				t.Errorf("usage error has %d lines: %q", lines, got.stderr)
			}
			for _, unwanted := range []string{"IMAGE OPTIONS", "GLOBAL OPTIONS", "DEFAULTS WHEN SAVING", "--prompt dall-e-2", "synthetic-private-prompt", "\x1b", "\a"} {
				if strings.Contains(got.stderr, unwanted) {
					t.Errorf("usage error exposed %q: %q", unwanted, got.stderr)
				}
			}
		})
	}
}

func TestMainHelpImageExtraArgumentsShowPromptExample(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	for _, args := range [][]string{
		{"synthetic-private-prompt"},
		{"synthetic-private-prompt", "with", "spaces"},
		{"--prompt", "synthetic-private-prompt", "extra"},
		{"synthetic-private-prompt\x1b]0;untrusted-title\a"},
	} {
		argv := append([]string{"./openai", "--base-url", server.URL, "images", "generate"}, args...)
		got := runMainDispatch(t, "bash", argv...)
		if got.code != 1 || got.stdout != "" {
			t.Fatalf("extra arguments changed exit status or stdout: %+v", got)
		}
		for _, want := range []string{"Unexpected extra arguments", "after --prompt and inside quotes", `./openai images generate --prompt "A tiny orange robot"`, "./openai images generate --help"} {
			if !strings.Contains(got.stderr, want) {
				t.Errorf("extra arguments guidance missing %q: %q", want, got.stderr)
			}
		}
		for _, unwanted := range []string{"synthetic-private-prompt", "untrusted-title", "\x1b", "\a"} {
			if strings.Contains(got.stderr, unwanted) {
				t.Errorf("extra arguments guidance exposed private input %q: %q", unwanted, got.stderr)
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("extra arguments made %d API requests", requests.Load())
	}
}

func TestMainHelpExplicitRoutesIgnoreRequestConfiguration(t *testing.T) {
	env := []string{
		"OPENAI_BASE_URL=not-a-request-url", "OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem",
	}
	for _, args := range [][]string{
		{"openai"}, {"openai", "help"}, {"openai", "help", "images", "generate"},
		{"openai", "help", "--all", "images", "generate"},
		{"openai", "--help"}, {"openai", "-h"}, {"openai", "images", "generate", "--help"},
		{"openai", "images", "generate", "-h"},
		{"openai", "--debug", "help", "--all", "images", "generate"},
		{"openai", "images", "generate", "--prompt", "example", "--help"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, args...)
		if got.code != 0 || got.stderr != "" || got.stdout == "" {
			t.Errorf("help with request configuration = %+v; args %q", got, args)
		}
	}
}

func TestMainHelpWithLeadingGlobalFlags(t *testing.T) {
	got := runMainDispatch(t, "bash", "openai", "--debug", "help")
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "help setup") {
		t.Fatalf("help with leading global flags = %+v", got)
	}
	got = runMainDispatch(t, "bash", "openai", "--debug", "help", "images", "generate")
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "~/Downloads/gpt-images/") {
		t.Fatalf("nested help with leading global flags = %+v", got)
	}
}

func TestMainHelpSetupIsReadOnly(t *testing.T) {
	env := []string{
		"OPENAI_BASE_URL=not-a-request-url",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/nonexistent/cert.pem",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/nonexistent/key.pem",
		"OPENAI_API_KEY=synthetic-key-must-not-appear-in-help",
	}
	for _, args := range [][]string{
		{"./openai", "help", "setup"},
		{"./openai", "help", "setup", "--help"},
		{"./openai", "--debug", "help", "setup"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("setup guide with request configuration = %+v; args %q", got, args)
		}
		for _, want := range []string{"read -rs OPENAI_API_KEY", "export OPENAI_API_KEY", "-AsSecureString", "./openai images generate --prompt", "guide only"} {
			if !strings.Contains(got.stdout, want) {
				t.Errorf("setup guide missing %q: %s", want, got.stdout)
			}
		}
		if strings.Contains(got.stdout, "synthetic-key-must-not-appear-in-help") {
			t.Fatal("setup guide printed an environment credential")
		}
	}
	got := runMainDispatch(t, "bash", "openai", "help", "setup", "unexpected")
	if got.code != 3 || got.stdout != "" {
		t.Fatalf("unexpected setup argument = %+v", got)
	}
}

func TestMainHelpDoesNotBypassRequestValidationForPromptValue(t *testing.T) {
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=not-a-request-url"},
		"openai", "images", "generate", "--prompt", "help")
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "OPENAI_BASE_URL") {
		t.Fatalf("prompt value bypassed request validation: %+v", got)
	}
}

func TestMainHelpPreservesPreviewFilename(t *testing.T) {
	got := runMainDispatch(t, "bash", "openai", "images", "preview", "help")
	// Subprocess output is piped. Reaching the preview's normal terminal check
	// proves the filename was dispatched, without opening a viewer or terminal.
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "image previews require terminal output") {
		t.Fatalf("preview filename help was intercepted: %+v", got)
	}
}

func TestMainHelpRejectsUnknownTopics(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "help", "imaginary"},
		{"openai", "help", "images", "imaginary"},
		{"openai", "help", "--all", "images", "imaginary"},
	} {
		got := runMainDispatch(t, "bash", args...)
		if got.code != 3 || got.stdout != "" || !strings.Contains(got.stderr, "Unknown help topic") {
			t.Errorf("unknown topic = %+v; args %q", got, args)
		}
	}
	if got := runMainDispatch(t, "bash", "openai", "imaginary"); got.code == 0 {
		t.Errorf("ordinary unknown command succeeded: %+v", got)
	}
}
