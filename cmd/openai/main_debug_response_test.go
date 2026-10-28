package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMainDebugResponseHeaderPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Configured"); got != "synthetic-request-value" {
			t.Errorf("request X-Configured = %q, want synthetic-request-value", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "synthetic-request-id")
		w.Header().Add("X-Gateway", "synthetic-response-first")
		w.Header().Add("X-Gateway", "synthetic-response-second")
		w.Header().Set("X-Configured", "synthetic-configured-response")
		if _, err := io.WriteString(w, `{"id":"test-model","object":"model","created":0,"owned_by":"test"}`); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_CUSTOM_HEADERS=X-Configured: synthetic-request-value"},
		"openai", "--base-url", server.URL, "--api-key", "sk-test", "--debug", "models", "retrieve", "--model", "test-model")
	if got.code != 0 || !strings.Contains(got.stdout, "test-model") {
		t.Fatalf("main debug request = %+v, want exit 0 and model output", got)
	}
	for _, value := range []string{"synthetic-request-value", "synthetic-response-first", "synthetic-response-second", "synthetic-configured-response"} {
		if strings.Contains(got.stdout+got.stderr, value) {
			t.Errorf("main debug output contains %q, want redacted value", value)
		}
	}
	for _, value := range []string{"X-Gateway: <REDACTED>", "X-Request-Id: synthetic-request-id", "Content-Type: application/json"} {
		if !strings.Contains(got.stderr, value) {
			t.Errorf("main debug output missing %q", value)
		}
	}
}
