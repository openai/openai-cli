package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainPickerCompletion(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	home := t.TempDir()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "OPENAI_BASE_URL=" + server.URL}
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			standard := runMainDispatchWithEnv(t, shell, env, "openai", "@completion", shell)
			if standard.code != 0 || standard.stderr != "" || standard.stdout == "" || strings.Contains(standard.stdout, "picker_disable") {
				t.Fatalf("standard completion = %+v", standard)
			}
			disabled := runMainDispatchWithEnv(t, shell, env, "openai", "@completion", shell, "--picker=false")
			if disabled != standard {
				t.Fatalf("explicit false changed ordinary completion: %+v", disabled)
			}
			for _, format := range []string{"text", "json"} {
				got := runMainDispatchWithEnv(t, shell, env, "openai", "--format-error", format, "@completion", shell, "--picker")
				if shell == "pwsh" {
					if got.code != 1 || got.stdout != "" {
						t.Fatalf("unsupported hook = %+v", got)
					}
					message := got.stderr
					if format == "json" {
						payload := decodeMainStructuredError(t, format, got.stderr)
						message, _ = payload["message"].(string)
					}
					if !strings.Contains(message, "press Enter") {
						t.Fatalf("missing Enter fallback: %q", message)
					}
				} else if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, standard.stdout+"\n") || !strings.Contains(got.stdout, "openai_picker_disable") {
					t.Fatalf("picker completion = %+v", got)
				}
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("completion made %d API requests", requests.Load())
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("completion wrote to the home/config directory: %v, %v", entries, err)
	}
}
