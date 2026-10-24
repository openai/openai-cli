package main

import (
	"strings"
	"testing"
)

func TestMainExploreRootHelpIntroducesOfflineSearch(t *testing.T) {
	for _, args := range [][]string{{"openai"}, {"openai", "--help"}, {"openai", "help"}, {"openai", "help", "--all"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url", "OPENAI_API_KEY="}, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("root help failed: %+v", got)
			}
			_, start, found := strings.Cut(got.stdout, "\nSTART HERE\n")
			if !found || !strings.HasPrefix(start, "  openai help setup ") || !strings.Contains(start, "Find image commands and examples offline.") {
				t.Fatalf("root help does not introduce setup and local search: %s", got.stdout)
			}
			var example string
			for _, line := range strings.Split(start, "\n") {
				if strings.HasPrefix(line, "  openai explore ") {
					example, _, _ = strings.Cut(strings.TrimSpace(line), "  ")
					break
				}
			}
			if example != "openai explore images" {
				t.Fatalf("missing copyable search example: %q", example)
			}
			search := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url", "OPENAI_API_KEY="}, strings.Fields(example)...)
			if search.code != 0 || search.stderr != "" || !strings.Contains(search.stdout, "MATCHING COMMANDS") || !strings.Contains(search.stdout, "openai images") {
				t.Fatalf("copied command did not run offline search: %+v", search)
			}
		})
	}
}

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

func TestMainExploreQueryWorksOfflineInPipes(t *testing.T) {
	for _, query := range [][]string{{"models", "list"}, {"MoDeLs LiSt"}} {
		got := runMainDispatchWithEnv(t, "zsh", []string{
			"OPENAI_API_KEY=", "OPENAI_BASE_URL=not%url", "TERM=dumb",
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/cert.pem",
			"OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic-private/key.pem",
		}, append([]string{"openai", "explore"}, query...)...)
		if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "MATCHING COMMANDS\n  openai models list\n") {
			t.Fatalf("offline ranked search failed: %+v", got)
		}
		if strings.Contains(got.stdout, "\x1b") || strings.Contains(got.stdout, "synthetic-private") {
			t.Fatalf("search exposed terminal escapes or API configuration: %+v", got)
		}
	}
	query := "no-results-synthetic-private-operand\x1b[31m"
	got := runMainDispatch(t, "zsh", "openai", "explore", query)
	if got.code != 0 || got.stderr != "" || got.stdout != "No matching commands. Try fewer words.\n" {
		t.Fatalf("empty search should succeed without echoing its query: %+v", got)
	}
	images := runMainDispatch(t, "zsh", "openai", "explore", "images")
	if images.code != 0 || !strings.Contains(images.stdout, "openai images generate") || strings.Contains(images.stdout, "openai explore images") {
		t.Fatalf("search should find image commands without suggesting the same search: %+v", images)
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
