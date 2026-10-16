package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Verify request encoding at the public CLI entrypoint without updating real keys.
func TestProjectAPIKeyUpdateRequestContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai").CombinedOutput(); err != nil {
		t.Fatalf("build CLI = %v (%s), want success", err, output)
	}

	tests := []struct {
		name, stdin, wantJSON string
		wantError             string
		nested                bool
		flags                 []string
	}{
		{name: "no update fields", wantError: "provide at least one of --scope or --expires-in-seconds"},
		{name: "empty YAML", stdin: "---\n{}\n", wantError: "provide at least one of --scope or --expires-in-seconds"},
		{name: "empty JSON", stdin: `{}`, wantError: "provide at least one of --scope or --expires-in-seconds"},
		{name: "unknown JSON", stdin: `{"unrelated":true}`, wantError: "provide at least one of --scope or --expires-in-seconds"},
		{name: "nested field JSON", stdin: `{"unrelated":{"scopes":[]}}`, wantError: "provide at least one of --scope or --expires-in-seconds"},
		{name: "unknown YAML nested command", stdin: "unrelated: true\n", nested: true, wantError: "provide at least one of --scope or --expires-in-seconds"},
		{name: "empty scopes nested command", stdin: `{"scopes":[]}`, nested: true, wantJSON: `{"scopes":[]}`},
		{name: "expiry YAML", stdin: "expires_in_seconds: 60\n", wantJSON: `{"expires_in_seconds":60}`},
		{name: "merged input", stdin: `{"scopes":[]}`, flags: []string{"--expires-in-seconds", "60"}, wantJSON: `{"scopes":[],"expires_in_seconds":60}`},
		{name: "expiry flags", flags: []string{"--expires-in-seconds", "60"}, wantJSON: `{"expires_in_seconds":60}`},
		{name: "scope flags", flags: []string{"--scope", "api.all.read"}, wantJSON: `{"scopes":["api.all.read"]}`},
		{name: "expiry JSON", stdin: `{"expires_in_seconds":60}`, wantJSON: `{"expires_in_seconds":60}`},
		{name: "scope JSON", stdin: `{"scopes":["api.files.read"]}`, wantJSON: `{"scopes":["api.files.read"]}`},
		// An explicit empty list selects All permissions; omission preserves restrictions.
		{name: "all permissions JSON", stdin: `{"scopes":[]}`, wantJSON: `{"scopes":[]}`},
		{name: "all permissions YAML", stdin: "scopes: []\n", wantJSON: `{"scopes":[]}`},
		{name: "combined flags", flags: []string{"--expires-in-seconds", "60", "--scope", "api.files.read"}, wantJSON: `{"expires_in_seconds":60,"scopes":["api.files.read"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var want map[string]any
			if test.wantError == "" {
				if err := json.Unmarshal([]byte(test.wantJSON), &want); err != nil {
					t.Fatalf("decode expected body %s = %v, want valid JSON", test.wantJSON, err)
				}
			}
			received := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				const path = "/organization/projects/proj_test/api_keys/key_test"
				if r.Method != http.MethodPost || r.URL.Path != path {
					t.Errorf("update route = %s %s, want POST %s", r.Method, r.URL.Path, path)
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-admin-key" {
					t.Error("update Authorization did not match the synthetic admin credential")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode update body = %v, want valid JSON", err)
				}
				select {
				case received <- body:
				default:
					t.Error("update sent more than one request, want exactly one")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"key_test","object":"organization.project.api_key"}`)
			}))
			t.Cleanup(server.Close)
			args := []string{"--base-url", server.URL, "admin:organization:projects:api-keys", "update", "--project-id", "proj_test", "--api-key-id", "key_test"}
			if test.nested {
				args = []string{"--base-url", server.URL, "admin", "organization", "projects", "api-keys", "update", "--project-id", "proj_test", "--api-key-id", "key_test"}
			}
			command := exec.CommandContext(t.Context(), binary, append(args, test.flags...)...)
			command.Stdin = strings.NewReader(test.stdin)
			// Isolate the subprocess from real credentials, proxies and mTLS settings.
			command.Env = []string{"FORCE_COLOR=0", "OPENAI_API_KEY=synthetic-project-key", "OPENAI_ADMIN_KEY=synthetic-admin-key"}
			output, err := command.CombinedOutput()
			if test.wantError != "" {
				if err == nil || !strings.Contains(string(output), test.wantError) {
					t.Fatalf("update %s = %v (%s), want error containing %q", test.name, err, output, test.wantError)
				}
				select {
				case <-received:
					t.Fatal("invalid update sent a request, want local rejection")
				default:
				}
				return
			}
			if err != nil {
				t.Fatalf("update %s = %v (%s), want success", test.name, err, output)
			}
			select {
			case got := <-received:
				if !reflect.DeepEqual(got, want) {
					t.Errorf("update %s body = %#v, want %#v", test.name, got, want)
				}
			default:
				t.Fatal("update sent no request, want exactly one")
			}
		})
	}
}
