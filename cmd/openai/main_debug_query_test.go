package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainDebugQueryRedaction(t *testing.T) {
	const email = "fake-private@example.test"
	file := filepath.Join(t.TempDir(), "email.txt")
	if err := os.WriteFile(file, []byte(email), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{email, "@" + file} {
		t.Run(value, func(t *testing.T) {
			queries := make(chan url.Values, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				queries <- r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"object":"list","data":[],"has_more":false}`)
			}))
			t.Cleanup(server.Close)
			result := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL,
				"OPENAI_ADMIN_KEY=sk-fake-debug-query-test",
			}, "openai", "--debug", "admin:organization:users", "list", "--email", value)
			if result.code != 0 {
				t.Fatalf("main(--email %q) exit=%d stderr=%q, want success", value, result.code, result.stderr)
			}
			select {
			case query := <-queries:
				if got := query.Get("emails[]"); got != email {
					t.Errorf("main(--email %q) sent query=%v, want emails[]=%q", value, query, email)
				}
			default:
				t.Fatal("main(list users) sent no request, want one local request")
			}
			if !strings.Contains(result.stderr, "GET /organization/users HTTP/1.1") {
				t.Errorf("main(--debug) stderr=%q, want request path without query", result.stderr)
			}
			for _, secret := range []string{email, url.QueryEscape(email), "sk-fake-debug-query-test", "emails"} {
				if strings.Contains(result.stderr, secret) {
					t.Errorf("main(--debug) stderr=%q, want no %q", result.stderr, secret)
				}
			}
		})
	}
}
