package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainDispatchOutputVerboseBeforeFailure(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("startup failure reached the API")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	home := t.TempDir()
	missing := filepath.Join(home, "synthetic-private-missing.pem")
	for _, config := range []struct {
		name, hint string
		env        []string
	}{
		{"base URL", "OPENAI_BASE_URL", []string{"OPENAI_BASE_URL=synthetic-private-invalid%url"}},
		{"mTLS", "mTLS", []string{"OPENAI_BASE_URL=" + server.URL,
			"OPENAI_MTLS_CLIENT_CERT_FILE=" + missing, "OPENAI_MTLS_CLIENT_KEY_FILE=" + missing}},
	} {
		for _, policy := range []struct {
			name          string
			flags         []string
			afterCommand  bool
			wantVerbose   bool
			structuredErr bool
		}{
			{name: "human", wantVerbose: true},
			{name: "nested flags", afterCommand: true, wantVerbose: true},
			{name: "quiet", flags: []string{"--quiet"}},
			{name: "inherited machine", flags: []string{"--format", "json"}, structuredErr: true},
			{name: "explicit machine", flags: []string{"--format-error", "json"}, structuredErr: true},
			{name: "extracted error", flags: []string{"--format-error", "text", "--transform-error", "message"}},
		} {
			t.Run(config.name+"/"+policy.name, func(t *testing.T) {
				env := append([]string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home,
					"XDG_CONFIG_HOME=" + home, "OPENAI_API_KEY=synthetic-private-startup-key"}, config.env...)
				args := append([]string{"openai"}, policy.flags...)
				// A value named help must not select a help command or enter the report.
				args = append(args, "models", "retrieve", "--model", "help")
				ordinary := runMainDispatchWithEnv(t, "bash", env, args...)
				if policy.afterCommand {
					args = append(args, "--verbose")
				} else {
					args = append([]string{"openai", "--verbose"}, args[1:]...)
				}
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				if ordinary.code != 1 || got.code != 1 || ordinary.stdout != "" || got.stdout != "" || connections.Load() != 0 {
					t.Fatalf("startup status, data, or network boundary changed: ordinary=%+v verbose=%+v; connections=%d", ordinary, got, connections.Load())
				}
				if !strings.Contains(got.stderr, config.hint) {
					t.Fatalf("startup error lost its safe configuration hint: %q", got.stderr)
				}
				if policy.wantVerbose {
					details, _ := removeVerboseElapsed(t, got.stderr)
					want := "Command: models retrieve\nFormat option: auto\nCommand result: failed\n" + ordinary.stderr
					if details != want {
						t.Fatalf("startup verbose must report once and preserve the error: got %q; want %q", details, want)
					}
				} else if got != ordinary {
					t.Fatalf("suppressed verbose changed the original error: ordinary=%+v verbose=%+v", ordinary, got)
				}
				if policy.structuredErr && !json.Valid([]byte(got.stderr)) {
					t.Fatalf("verbose corrupted the complete JSON error: %q", got.stderr)
				}
				for _, private := range []string{home, missing, server.URL, "synthetic-private-", "\x1b"} {
					if strings.Contains(got.stderr, private) {
						t.Fatalf("startup diagnostics exposed configuration data: %q", got.stderr)
					}
				}
			})
		}
	}
}
