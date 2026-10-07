package main

import (
	"strings"
	"testing"
)

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
		got := runMainDispatch(t, style, mainCompletionArgs(style, "help", "--all", "responses", "cr")...)
		if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "create") || strings.Contains(got.stdout, "setup") {
			t.Errorf("%s help completion failed: %+v", style, got)
		}
		for _, args := range [][]string{{"files", "create", "--file", ""}, {"audio", "transcriptions", "create", "--file", ""}, {"images", "edit", "--image", ""}} {
			got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1"}, mainCompletionArgs(style, args...)...)
			if got.code != 10 || got.stdout != "" || got.stderr != "" {
				t.Errorf("%s file completion failed for %q: %+v", style, args, got)
			}
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
