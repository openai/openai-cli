package main

import (
	"strings"
	"testing"
)

func TestMainExploreStaysLocalWithInvalidAPIConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"explore"},
		{"--format", "text", "explore"},
		{"explore", "--shell-integration", "--shell", "zsh"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "zsh", []string{
				"OPENAI_BASE_URL=not%url",
				"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/cert.pem",
				"OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic-private/key.pem",
			}, append([]string{"openai"}, args...)...)
			if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "terminal") {
				t.Fatalf("expected terminal requirement instead of API setup: %+v", got)
			}
			if strings.Contains(got.stderr, "synthetic-private") || strings.Contains(got.stderr, "OPENAI_BASE_URL") {
				t.Fatalf("local search used API configuration: %+v", got)
			}
		})
	}
}

func TestMainExploreFlagValueDoesNotBypassAPIConfiguration(t *testing.T) {
	got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url"},
		"openai", "models", "retrieve", "--model", "explore")
	if got.code == 0 || !strings.Contains(got.stderr, "OPENAI_BASE_URL") {
		t.Fatalf("an API argument bypassed request configuration: %+v", got)
	}
}

func TestMainExploreHelpAndStructuredErrors(t *testing.T) {
	help := runMainDispatch(t, "zsh", "openai", "explore", "--help")
	if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, "openai explore") {
		t.Fatalf("explore help failed: %+v", help)
	}
	got := runMainDispatch(t, "zsh", "openai", "--format", "json", "explore")
	if got.code != 2 || got.stdout != "" {
		t.Fatalf("unexpected structured failure: %+v", got)
	}
	payload := decodeMainStructuredError(t, "json", got.stderr)
	if !strings.Contains(payload["message"].(string), "format") {
		t.Fatalf("missing format guidance: %#v", payload)
	}
}

func TestMainExploreExplicitMachineErrorsStayStructured(t *testing.T) {
	got := runMainDispatch(t, "zsh", "openai", "--format-error", "json", "explore")
	if got.code != 2 || got.stdout != "" {
		t.Fatalf("unexpected failure: %+v", got)
	}
	payload := decodeMainStructuredError(t, "json", got.stderr)
	if !strings.Contains(payload["message"].(string), "error output") {
		t.Fatalf("missing error-format guidance: %#v", payload)
	}
}
