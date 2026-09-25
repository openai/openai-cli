package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainLocalErrorGuidance(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"extra argument", []string{"models", "list", "synthetic-private-extra"}, []string{"Unexpected extra arguments.", "--help"}},
		{"missing shell", []string{"@completion"}, []string{"Choose a shell", "openai @completion", "bash", "fish", "pwsh", "zsh"}},
		{"unknown shell", []string{"@completion", "synthetic-private-shell"}, []string{"Unsupported completion shell.", "openai @completion", "bash", "fish", "pwsh", "zsh"}},
		{"invalid format", []string{"--format", "synthetic-private-format", "models", "list"}, []string{"Invalid output format.", "auto, text, explore, json, jsonl, pretty, raw, yaml"}},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				args := append([]string{"openai", "--base-url", server.URL, "--format-error", format}, test.args...)
				got := runMainDispatch(t, "bash", args...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("local error = %+v; want exit 1 and stderr only", got)
				}
				message := got.stderr
				if format == "json" {
					payload := decodeMainStructuredError(t, format, got.stderr)
					message, _ = payload["message"].(string)
				}
				for _, want := range test.want {
					if !strings.Contains(message, want) {
						t.Errorf("message = %q, want %q", message, want)
					}
				}
				if strings.Contains(got.stderr, "synthetic-private-") {
					t.Errorf("local diagnostic echoed rejected arguments: %q", got.stderr)
				}
			})
		}
	}
	t.Run("invalid error format uses text", func(t *testing.T) {
		got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--format-error", "synthetic-private-format", "models", "list")
		if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "Invalid output format.") || strings.Contains(got.stderr, "synthetic-private-") {
			t.Errorf("invalid error format = %+v", got)
		}
	})
	if requests.Load() != 0 {
		t.Errorf("invalid local arguments sent %d requests, want 0", requests.Load())
	}
}
