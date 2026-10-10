package main

import (
	"os"
	"strings"
	"testing"
)

func TestMainWelcomeRedirectedOutputMatchesHelp(t *testing.T) {
	want := runMainDispatch(t, "bash", "openai", "--help")
	if want.code != 0 || want.stderr != "" || !strings.HasPrefix(want.stdout, "NAME:\n   openai\n") {
		t.Fatalf("root help control failed: %+v", want)
	}
	for _, argv := range [][]string{nil, {"openai"}, {""}} {
		if got := runMainDispatch(t, "bash", argv...); got != want {
			t.Errorf("redirected bare invocation changed help: argv=%q got=%+v", argv, got)
		}
	}
}

func TestMainWelcomeExplicitArgumentsStayPlain(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"help", "--all"}, {"--"},
		{"--version"}, {"--debug", "--help"}, {"--format", "json"},
		{"--format", "json", "--help"}, {"--format", "jsonl", "help"},
		{"--format-error", "json", "--help"}, {"--raw-output", "--help"},
		{"--transform", "id", "--help"}, {"models"}, {"models", "--help"},
		{"help", "models"}, {"models", "list", "--help"},
		{"--quiet"}, {"--verbose"}, {"--quiet", "--verbose"}, {"--verbose", "--quiet"},
		{"--quiet=false", "--verbose=false"}, {"--verbose", "help"},
		{"--quiet", "--format", "json", "--help"},
		{"files", "upload", "--help"}, {"files", "get", "--help"}, {"files", "download", "--help"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
			if got.code != 0 || got.stderr != "" || got.stdout == "" {
				t.Fatalf("explicit invocation failed: %+v", got)
			}
			for _, marker := range []string{">_ OpenAI CLI", "What are we making today?"} {
				if strings.Contains(got.stdout+got.stderr, marker) {
					t.Errorf("explicit invocation contains welcome marker %q", marker)
				}
			}
		})
	}
}

func TestMainWelcomeBareInvocationStaysOffline(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	for _, baseURL := range []string{server.URL, "not%url"} {
		t.Run(baseURL, func(t *testing.T) {
			home := t.TempDir()
			env := []string{
				"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
				"XDG_CACHE_HOME=" + home, "APPDATA=" + home,
				"OPENAI_BASE_URL=" + baseURL, "OPENAI_API_KEY=", "OPENAI_ADMIN_KEY=",
				"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic/missing-cert.pem",
				"OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic/missing-key.pem",
			}
			want := runMainDispatchWithEnv(t, "bash", env, "openai", "--help")
			got := runMainDispatchWithEnv(t, "bash", env, "openai")
			if got.code != 0 || got.stderr != "" || got.stdout == "" || got != want {
				t.Fatalf("bare invocation depends on request configuration: got=%+v want=%+v", got, want)
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("redirected bare invocation wrote state: entries=%v err=%v", entries, err)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("bare invocation made %d requests", got)
	}
}
