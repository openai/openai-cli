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
			got := runMainDispatch(t, style, mainCompletionArgs(style, args...)...)
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
