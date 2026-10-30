package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainFineTuningModelLimitsRemoved(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	home := t.TempDir()
	env := []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-model-limits-removed",
		"OPENAI_ADMIN_KEY=sk-fake-admin-unused", "NO_COLOR=1", "FORCE_COLOR=0", "CI=1",
	}
	const privateOperand = "synthetic-private-model-limits-operand"
	for _, tc := range []struct {
		name, prefix string
		args         []string
	}{
		{"command", "Unknown command.", []string{"fine-tuning", "model-limits"}},
		{"private operand", "Unknown command.", []string{"fine-tuning", "model-limits", privateOperand}},
		{"short help", "Unknown help topic.", []string{"fine-tuning", "model-limits", "--help"}},
		{"full help", "Unknown help topic.", []string{"help", "fine-tuning", "model-limits"}},
	} {
		for _, format := range []string{"auto", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				argv := append([]string{"openai", "--format-error=" + format}, tc.args...)
				got := runMainDispatchWithEnv(t, "bash", env, argv...)
				if got.code != 3 || got.stdout != "" {
					t.Fatalf("removed command returned code=%d stdout=%q stderr=%q", got.code, got.stdout, got.stderr)
				}
				message := strings.TrimSpace(got.stderr)
				if format == "json" {
					payload := decodeMainStructuredError(t, format, got.stderr)
					if len(payload) != 1 {
						t.Fatalf("local rejection included unexpected fields: %#v", payload)
					}
					message, _ = payload["message"].(string)
				}
				if !strings.HasPrefix(message, tc.prefix) || !strings.Contains(message, "openai help") {
					t.Errorf("expected safe command recovery, got %q", message)
				}
				for _, private := range []string{privateOperand, "sk-fake-model-limits-removed", "sk-fake-admin-unused"} {
					if strings.Contains(got.stderr, private) {
						t.Errorf("local rejection exposed synthetic private input %q", private)
					}
				}
			})
		}
	}
	if count := requests.Load(); count != 0 {
		t.Fatalf("removed command sent %d requests", count)
	}
}
