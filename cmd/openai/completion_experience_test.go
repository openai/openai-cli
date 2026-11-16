package main

import (
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
)

func completionTestTopic() string {
	if runtime.GOOS == "windows" {
		return `"@completion"`
	}
	return "@completion"
}

func TestMainCompletionSetupHelp(t *testing.T) {
	var first string
	for _, args := range [][]string{
		{"openai", "@completion", "--help"},
		{"openai", "@completion", "-h"},
		{"openai", "help", "@completion"},
		{"openai", "help", "--all=false", "@completion"},
		{"openai", "--help", "--all=false", "@completion"},
		{"openai", "--all=false", "@completion", "--help"},
		{"openai", "--all=false", "@completion", "zsh", "--install-suggestions", "--help"},
	} {
		got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=invalid"}, args...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("help %q: %+v", args, got)
		}
		if first != "" && got.stdout != first {
			t.Fatalf("help %q differs from @completion --help: %s", args, got.stdout)
		}
		first = got.stdout
	}
	topic := completionTestTopic()
	for _, text := range []string{"openai explore", "Select a command and press Enter.", "copy it to your prompt", "openai help --all " + topic} {
		if !strings.Contains(first, text) {
			t.Errorf("missing %q in %s", text, first)
		}
	}
	for _, unrelated := range []string{"FLAGS", "--install-suggestions", "--profile", "--picker", "--suggestions", "--debug"} {
		if strings.Contains(first, unrelated) {
			t.Errorf("unexpected %q in completion help", unrelated)
		}
	}
	full := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=invalid"}, "openai", "help", "--all", "@completion")
	if full.code != 0 || full.stderr != "" {
		t.Fatalf("full completion reference: %+v", full)
	}
	for _, args := range [][]string{
		{"openai", "--help", "--all", "@completion"},
		{"openai", "--all", "@completion", "--help"},
		{"openai", "@completion", "zsh", "--install-suggestions", "--help"},
		{"openai", "@completion", "--help", "bash", "--install-picker", "--profile", "startup"},
	} {
		if got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=invalid"}, args...); got != full {
			t.Fatalf("full help %q differs from help --all @completion: %+v", args, got)
		}
	}
	for _, text := range []string{"openai " + topic + " [SHELL] [FLAGS]", "bash, zsh, fish, or pwsh", "openai " + topic + " zsh",
		`openai @completion zsh --install-suggestions --profile "${ZDOTDIR-$HOME}/.zshrc"`, "\nFLAGS\n", "--help, -h"} {
		if !strings.Contains(full.stdout, text) {
			t.Errorf("full completion reference omitted %q", text)
		}
	}
	app.ConfigureCommand(commands.Command)
	_, flags, _ := strings.Cut(full.stdout, "\nFLAGS\n")
	if !strings.Contains(strings.Join(strings.Fields(flags), " "), "--automatic Requires --install-picker. Omit the shell name and --profile.") {
		t.Error("automatic picker setup must identify the required flag and incompatible overrides")
	}
	for _, flag := range commands.Command.Command("@completion").VisibleFlags() {
		for _, name := range flag.Names() {
			option := "--" + name
			if len(name) == 1 {
				option = "-" + name
			}
			label := regexp.MustCompile(`(?m)^  (?:\S+, )*` + regexp.QuoteMeta(option) + `(?:=|,|\s|$)`)
			if !label.MatchString(flags) {
				t.Errorf("full completion reference lacks a flag row for %s", option)
			}
		}
	}
	for _, unrelated := range []string{"\x1b", "--api-key", "--auto-suggestions", "--suggestions-only", "no longer needed"} {
		if strings.Contains(first+full.stdout, unrelated) {
			t.Errorf("unexpected %q in completion help", unrelated)
		}
	}
}

func TestMainCompletionStaysLocalWithInvalidRequestConfiguration(t *testing.T) {
	env := []string{
		"OPENAI_BASE_URL=invalid-completion-url",
		"OPENAI_CUSTOM_HEADERS=invalid-completion-headers",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/missing/completion-cert.pem",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/missing/completion-key.pem",
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "models", "")...)
		if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "list") {
			t.Errorf("%s completion depends on request configuration: %+v", style, got)
		}
	}
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "models", "retrieve", "--model", "__complete")
	if got.code == 0 || !strings.Contains(got.stderr, "OPENAI_BASE_URL") {
		t.Errorf("ordinary request bypassed validation: %+v", got)
	}
}

func TestMainCompletionDescriptionsStayInOneRecord(t *testing.T) {
	want := map[string]bool{"create": true, "retrieve": true, "delete": true, "cancel": true, "compact": true, "input-items": true, "input-tokens": true}
	for _, style := range []string{"zsh", "fish"} {
		got := runMainDispatch(t, style, mainCompletionArgs(style, "responses", "")...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("%s completion failed: %+v", style, got)
		}
		lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
		if len(lines) != len(want) {
			t.Errorf("%s emitted %d completion records for %d commands: %q", style, len(lines), len(want), got.stdout)
		}
		separator := ":"
		if style == "fish" {
			separator = "\t"
		}
		seen := make(map[string]bool)
		for _, line := range lines {
			name, _, _ := strings.Cut(line, separator)
			if !want[name] || seen[name] {
				t.Errorf("%s emitted an unexpected or duplicate candidate %q", style, line)
			}
			seen[name] = true
		}
	}
}

func TestMainCompletionHelpAndFileInputs(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, args := range [][]string{{"files", "create", "--file", ""}, {"audio", "transcriptions", "create", "--file", ""}, {"images", "edit", "--image", ""}} {
			got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1"}, mainCompletionArgs(style, args...)...)
			if got.code != 10 || got.stdout != "" || got.stderr != "" {
				t.Errorf("%s file completion failed for %q: %+v", style, args, got)
			}
		}
	}
}

func TestMainCompletionOffersPipedRequestAlongsideFiles(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		marker string
		code   int
		prefix string
	}{
		{[]string{"responses", "create", "--request-file", ""}, "1", 12, ""},
		{[]string{"chat", "completions", "create", "--request-file", "-"}, "1", 12, ""},
		{[]string{"chat:completions", "create", "--request-file=-"}, "1", 12, "--request-file=\n"},
		{[]string{"responses", "create", "--request-file", "req"}, "1", 10, ""},
		{[]string{"responses", "create", "--request-file=-draft"}, "1", 10, "--request-file=\n"},
		{[]string{"responses", "create", "--request-file", ""}, "", 10, ""},
		{[]string{"responses", "create", "--request-file="}, "", 10, "--request-file=\n"},
		{[]string{"--mtls-client-cert-file", ""}, "1", 10, ""},
	} {
		env := []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1", "OPENAI_CLI_COMPLETION_STDIN_PATH=" + tc.marker}
		got := runMainDispatchWithEnv(t, "zsh", env, mainCompletionArgs("zsh", tc.args...)...)
		if got != (mainDispatchResult{code: tc.code, stdout: tc.prefix}) {
			t.Errorf("stdin-path marker %q, args %q: %+v", tc.marker, tc.args, got)
		}
	}
}

func TestMainCompletionPreservesTypedDoubleDash(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(style, func(t *testing.T) {
			got := runMainDispatch(t, style, mainCompletionArgs(style, "--")...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "--format") {
				t.Fatalf("long flag prefix lost: %+v", got)
			}
			for _, line := range strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n") {
				if !strings.HasPrefix(line, "--") {
					t.Fatalf("flag prefix offered a command: %q", line)
				}
			}
			got = runMainDispatch(t, style, mainCompletionArgs(style, "--", "--fo")...)
			if got != (mainDispatchResult{}) {
				t.Fatalf("end-of-options marker offered flags: %+v", got)
			}
		})
	}
}

func TestMainCompletionAssignedFileValues(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, args := range [][]string{
			{"audio", "transcribe", "--file=assets/lo"}, {"transcribe", "--file=assets/lo"},
			{"files", "upload", "--file=assets/lo"}, {"images", "edit", "--image=assets/a=b"},
			{"audio", "transcriptions", "create", "--file="}, {"audio:transcriptions", "create", "--file=assets/lo"},
			{"--mtls-client-cert-file=assets/lo"}, {"models", "list", "--mtls-client-key-file=assets/lo"},
		} {
			got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1"}, mainCompletionArgs(style, args...)...)
			name, _, _ := strings.Cut(args[len(args)-1], "=")
			if got.code != 10 || got.stdout != name+"=\n" || got.stderr != "" {
				t.Errorf("%s assigned file completion failed for %q: %+v", style, args, got)
			}
		}
		for _, tc := range []struct {
			args []string
			code int
		}{
			{[]string{"transcribe", "--file", "--file=assets/lo"}, 10},
			{[]string{"transcribe", "--model=assets/lo"}, 11},
			{[]string{"transcribe", "--unknown=assets/lo"}, 11},
			{[]string{"transcribe", "--", "--file=assets/lo"}, 0},
		} {
			got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
			if got.code != tc.code || got.stdout != "" || got.stderr != "" {
				t.Errorf("%s assignment control failed for %q: %+v", style, tc.args, got)
			}
		}
	}
}

func TestMainCompletionRetainsOlderAdapterBehavior(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, marker := range []string{"", "0", "true", "2"} {
			for _, args := range [][]string{
				{"audio:transcriptions", "create", "--file", "assets/lo"},
				{"audio:transcriptions", "create", "--file=assets/lo"},
				{"transcribe", "--file", "assets/lo"},
				{"images", "edit", "--image", "assets/lo"},
				{"--mtls-client-cert-file=assets/lo"},
			} {
				env := []string(nil)
				if marker != "" {
					env = []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=" + marker}
				}
				got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, args...)...)
				if got != (mainDispatchResult{code: 11}) {
					t.Errorf("%s marker %q changed older adapter behavior for %q: %+v", style, marker, args, got)
				}
			}
		}
		got := runMainDispatch(t, style, mainCompletionArgs(style, "--mtls-client-cert-file", "assets/lo")...)
		if got != (mainDispatchResult{code: 10}) {
			t.Errorf("%s lost legacy TakesFile completion: %+v", style, got)
		}
	}
}
