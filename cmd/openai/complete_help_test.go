package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainCompleteHelpAfterOptions(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "help must remain offline", http.StatusBadRequest)
	}))
	defer server.Close()
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY="}
	for _, tc := range []struct {
		path, flags []string
	}{
		{[]string{"images", "generate"}, []string{"--size", "1024x1024", "--prompt", "--help"}},
		{[]string{"images", "edit"}, []string{"--image", "missing-help-fixture.png", "--prompt", "help"}},
		{[]string{"images", "preview"}, []string{"missing-help-fixture.png"}},
		{[]string{"models", "retrieve"}, []string{"--model", "help"}},
		{[]string{"models", "list"}, []string{"--max-items", "0", "--format", "json"}},
		{[]string{"admin", "organization", "audit-logs", "list"}, []string{"--max-items", "1"}},
	} {
		t.Run(strings.Join(tc.path, "/"), func(t *testing.T) {
			base := append([]string{"openai"}, tc.path...)
			want := runMainDispatchWithEnv(t, "bash", env, append(base, "--help")...)
			if want.code != 0 || want.stderr != "" {
				t.Fatalf("canonical help failed: %+v", want)
			}
			for _, trigger := range []string{"--help", "-h", "--help=true", "-h=true"} {
				args := append(append(append([]string{}, base...), tc.flags...), trigger)
				if got := runMainDispatchWithEnv(t, "bash", env, args...); got != want {
					t.Errorf("help after options changed the page: args=%q got=%+v", args, got)
				}
				args = append(append([]string{"openai", trigger}, tc.path...), tc.flags...)
				if got := runMainDispatchWithEnv(t, "bash", env, args...); got != want {
					t.Errorf("help before command changed the page: args=%q got=%+v", args, got)
				}
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("help made %d requests", requests.Load())
	}
}

func TestMainCompleteHelpLegacyMigration(t *testing.T) {
	for _, path := range [][]string{nil, {"images", "generate"}, {"admin", "organization", "audit-logs", "list"}} {
		t.Run(strings.Join(append([]string{"root"}, path...), "/"), func(t *testing.T) {
			want := runMainDispatch(t, "bash", append([]string{"openai", "help"}, path...)...)
			if want.code != 0 || want.stderr != "" {
				t.Fatalf("canonical help failed: %+v", want)
			}
			routes := [][]string{
				append([]string{"openai", "help", "--all"}, path...),
				append([]string{"openai", "help", "-all"}, path...),
				append([]string{"openai", "help", "--all=false"}, path...),
				append(append([]string{"openai", "help"}, path...), "--all"),
				append([]string{"openai", "--help", "--all"}, path...),
			}
			for _, args := range routes {
				got := runMainDispatch(t, "bash", args...)
				if got.code != 0 || got.stderr != "" || !strings.HasSuffix(got.stdout, want.stdout) {
					t.Errorf("legacy help lost canonical content: args=%q got=%+v", args, got)
					continue
				}
				notice := strings.TrimSuffix(got.stdout, want.stdout)
				replacement := strings.TrimSpace("openai help " + strings.Join(path, " "))
				if !strings.Contains(notice, replacement) || strings.Count(notice, "--all") != 1 {
					t.Errorf("legacy help lacks one precise migration notice: %q", notice)
				}
			}
		})
	}
}

func TestMainCompleteHelpTypedLegacyCompletion(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, flag := range []string{"--all", "-all", "--all=false", "-all=true"} {
			got := runMainDispatch(t, style, mainCompletionArgs(style, "help", flag, "responses", "cr")...)
			if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "create") {
				t.Errorf("%s lost supported legacy %q completion: %+v", style, flag, got)
			}
		}
		for _, args := range [][]string{
			{"help", "--all=invalid", "responses", "cr"},
			{"help", "-all=invalid", "responses", "cr"},
			{"help", "---all", "responses", "cr"},
			{"help", "--", "--all", "responses", "cr"},
		} {
			got := runMainDispatch(t, style, mainCompletionArgs(style, args...)...)
			if got.code != 11 || got.stderr != "" || got.stdout != "" {
				t.Errorf("%s completed malformed or literal arguments %q: %+v", style, args, got)
			}
		}
	}
}

func TestMainCompleteHelpRejectsMalformedLegacyPrefix(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "---all", "--help"},
		{"openai", "---all=false", "--help"},
		{"openai", "----all", "--help", "images", "generate"},
	} {
		got := runMainDispatch(t, "bash", args...)
		if got.code != 1 || got.stdout != "" || got.stderr == "" {
			t.Errorf("malformed legacy flag bypassed parsing: args=%q got=%+v", args, got)
		}
	}
}

func TestMainCompleteHelpCompletionHidesLegacyFlag(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, path := range [][]string{{"help"}, {"help", "images", "generate"}} {
			got := runMainDispatch(t, style, mainCompletionArgs(style, append(path, "--")...)...)
			if got.code != 0 || got.stderr != "" || strings.Contains(got.stdout, "--all") {
				t.Errorf("%s advertises retired help flag: %+v", style, got)
			}
		}
		got := runMainDispatch(t, style, mainCompletionArgs(style, "images", "models", "--")...)
		if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "--all") {
			t.Errorf("%s lost image-model discovery flag: %+v", style, got)
		}
	}
}

func TestMainCompleteHelpGlobalMetadata(t *testing.T) {
	got := runMainDispatch(t, "bash", "openai", "--help")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("root help failed: %+v", got)
	}
	for _, heading := range []string{
		"--api-key KEY", "--admin-api-key KEY", "--organization ID", "--project ID", "--webhook-secret SECRET",
		"--header, -H 'NAME: VALUE'", "--base-url URL", "--mtls-client-cert-file PATH", "--mtls-client-key-file PATH",
	} {
		if !strings.Contains(got.stdout, "\n   "+heading+"\n") {
			t.Errorf("missing semantic flag heading %q", heading)
		}
	}
	for _, line := range []string{
		"Env: OPENAI_API_KEY", "Env: OPENAI_ADMIN_KEY", "Env: OPENAI_BASE_URL",
		"Env: OPENAI_ORG_ID", "Env: OPENAI_PROJECT_ID", "Env: OPENAI_WEBHOOK_SECRET",
		"Env: OPENAI_MTLS_CLIENT_CERT_FILE", "Env: OPENAI_MTLS_CLIENT_KEY_FILE",
		"Default: https://api.openai.com/v1",
	} {
		if !strings.Contains(got.stdout, "\n      "+line+"\n") {
			t.Errorf("missing separate metadata line %q", line)
		}
	}
	if strings.Contains(got.stdout, "(default:") || strings.Contains(got.stdout, "[$OPENAI_") {
		t.Error("default or environment metadata remains embedded in a description")
	}
}
