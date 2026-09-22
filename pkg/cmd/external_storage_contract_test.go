package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the public entrypoint, including the real SDK's admin authentication
// and request overlays. All subprocess credentials and responses are synthetic.
func TestExternalStorageContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai").CombinedOutput(); err != nil {
		t.Fatalf("build CLI = %v (%s), want success", err, output)
	}
	run := func(t *testing.T, endpoint, format, stdin string, admin bool, args ...string) ([]byte, error) {
		t.Helper()
		flags := []string{"--base-url", endpoint, "--format", format, "admin:organization:external-storage"}
		command := exec.CommandContext(t.Context(), binary, append(flags, args...)...)
		command.Stdin = strings.NewReader(stdin)
		// Do not inherit local credentials, proxy settings, or mTLS configuration.
		command.Env = []string{"FORCE_COLOR=0", "OPENAI_API_KEY=synthetic-project-key"}
		if admin {
			command.Env = append(command.Env, "OPENAI_ADMIN_KEY=synthetic-admin-key")
		}
		return command.CombinedOutput()
	}
	checkAuth := func(t *testing.T, r *http.Request) {
		t.Helper()
		if got := r.Header.Get("Authorization"); got != "Bearer synthetic-admin-key" {
			t.Errorf("external storage Authorization = %q, want synthetic admin bearer", got)
		}
	}
	response := `{"id":"extstorage_test","object":"external_storage","future_field":{"kept":true}}`

	t.Run("provider request bodies", func(t *testing.T) {
		tests := []struct {
			name, stdin, body string
			flags             []string
		}{
			{
				name:  "AWS JSON flag",
				flags: []string{"--project-id", "proj_test", "--provider", `{"type":"aws","bucket":"test-bucket","role_arn":"arn:aws:iam::000000000000:role/test"}`},
				body:  `{"project_id":"proj_test","provider":{"type":"aws","bucket":"test-bucket","role_arn":"arn:aws:iam::000000000000:role/test"}}`,
			},
			{
				name:  "Azure YAML stdin",
				stdin: "project_id: proj_test\nprovider:\n  type: azure\n  tenant_id: tenant_test\n  subscription_id: subscription_test\n  resource_group: group_test\n  account_name: account_test\n  container: container_test\n",
				body:  `{"project_id":"proj_test","provider":{"type":"azure","tenant_id":"tenant_test","subscription_id":"subscription_test","resource_group":"group_test","account_name":"account_test","container":"container_test"}}`,
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					checkAuth(t, r)
					if r.Method != http.MethodPost || r.URL.Path != "/organization/external_storage" {
						t.Errorf("create route = %s %s, want POST /organization/external_storage", r.Method, r.URL.Path)
					}
					var got, want any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Errorf("decode create body = %v, want valid JSON", err)
					}
					if err := json.Unmarshal([]byte(test.body), &want); err != nil {
						t.Errorf("decode expected body = %v, want valid fixture", err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("create body = %#v, want %#v", got, want)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, response)
				}))
				t.Cleanup(server.Close)
				output, err := run(t, server.URL, "raw", test.stdin, true, append([]string{"create"}, test.flags...)...)
				if err != nil {
					t.Fatalf("create %s = %v (%s), want success", test.name, err, output)
				}
				if got := requests.Load(); got != 1 {
					t.Errorf("create request count = %d, want 1", got)
				}
				if got := strings.TrimSpace(string(output)); got != response {
					t.Errorf("create raw output = %s, want unchanged %s", got, response)
				}
			})
		}
	})

	t.Run("ID routes", func(t *testing.T) {
		for _, test := range []struct{ action, method, suffix string }{
			{"retrieve", http.MethodGet, ""},
			{"delete", http.MethodDelete, ""},
			{"validate", http.MethodPost, "/validate"},
		} {
			t.Run(test.action, func(t *testing.T) {
				const id = "storage/with ?#"
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					checkAuth(t, r)
					wantPath := "/organization/external_storage/" + url.PathEscape(id) + test.suffix
					if r.Method != test.method || r.URL.EscapedPath() != wantPath || r.URL.RawQuery != "" {
						t.Errorf("%s route = %s %s, want %s %s", test.action, r.Method, r.URL.RequestURI(), test.method, wantPath)
					}
					body, err := io.ReadAll(r.Body)
					if err != nil || len(body) != 0 {
						t.Errorf("%s body = %q, error %v, want empty", test.action, body, err)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, response)
				}))
				t.Cleanup(server.Close)
				output, err := run(t, server.URL, "raw", "", true, test.action, "--external-storage-id", id)
				if err != nil {
					t.Fatalf("%s = %v (%s), want success", test.action, err, output)
				}
				if got := requests.Load(); got != 1 {
					t.Errorf("%s request count = %d, want 1", test.action, got)
				}
				if got := strings.TrimSpace(string(output)); got != response {
					t.Errorf("%s raw output = %s, want unchanged %s", test.action, got, response)
				}
			})
		}
	})

	t.Run("pagination and filters", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page := requests.Add(1)
			checkAuth(t, r)
			after := "extstorage_start"
			if page == 2 {
				after = "extstorage_1"
			}
			wantQuery := url.Values{"after": {after}, "limit": {"1"}, "order": {"asc"}, "project_id": {"proj_test"}}
			if r.Method != http.MethodGet || r.URL.Path != "/organization/external_storage" || !reflect.DeepEqual(r.URL.Query(), wantQuery) || page > 2 {
				t.Errorf("list page %d = %s %s, want GET external_storage with %v", page, r.Method, r.URL.RequestURI(), wantQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"object":"list","data":[{"id":"extstorage_%d","object":"external_storage","future_field":true}],"has_more":true,"last_id":"extstorage_%d"}`, page, page)
		}))
		t.Cleanup(server.Close)
		output, err := run(t, server.URL, "jsonl", "", true, "list", "--after", "extstorage_start", "--limit", "1", "--order", "asc", "--project-id", "proj_test", "--max-items", "2")
		if err != nil {
			t.Fatalf("paginated list = %v (%s), want success", err, output)
		}
		if got := requests.Load(); got != 2 {
			t.Errorf("list request count = %d, want 2", got)
		}
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) != 2 {
			t.Fatalf("list JSONL = %s, want two items", output)
		}
		for i, line := range lines {
			var got map[string]any
			if err := json.Unmarshal([]byte(line), &got); err != nil {
				t.Fatalf("list item %d = %q (%v), want JSON", i, line, err)
			}
			if got["id"] != fmt.Sprintf("extstorage_%d", i+1) || got["future_field"] != true {
				t.Errorf("list item %d = %v, want matching ID and preserved future_field", i, got)
			}
		}
	})

	t.Run("raw list preserves envelope without paging", func(t *testing.T) {
		const page = `{"object":"list","data":[{"id":"extstorage_1","future_field":true}],"has_more":true,"last_id":"extstorage_1","future_envelope":true}`
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			checkAuth(t, r)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, page)
		}))
		t.Cleanup(server.Close)
		output, err := run(t, server.URL, "raw", "", true, "list")
		if err != nil {
			t.Fatalf("raw list = %v (%s), want success", err, output)
		}
		if got := requests.Load(); got != 1 {
			t.Errorf("raw list request count = %d, want 1", got)
		}
		if got := strings.TrimSpace(string(output)); got != page {
			t.Errorf("raw list = %s, want unchanged %s", got, page)
		}
	})

	t.Run("no project key fallback", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("admin operation without admin key Authorization = %q, want empty", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"An admin key is required","type":"authentication_error"}}`)
		}))
		t.Cleanup(server.Close)
		output, err := run(t, server.URL, "raw", "", false, "list")
		if err == nil {
			t.Errorf("admin operation without admin key = success (%s), want API authentication error", output)
		}
		if got := requests.Load(); got != 1 {
			t.Errorf("unauthenticated request count = %d, want 1", got)
		}
	})

	t.Run("reject before request", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			admin bool
			args  []string
		}{
			{"missing project", true, []string{"create", "--provider", `{"type":"aws"}`}},
			{"missing provider", true, []string{"create", "--project-id", "proj_test"}},
			{"malformed provider", true, []string{"create", "--project-id", "proj_test", "--provider", "{invalid"}},
			{"missing retrieve ID", true, []string{"retrieve"}},
			{"missing delete ID", true, []string{"delete"}},
			{"missing validate ID", true, []string{"validate"}},
			{"extra arguments", true, []string{"list", "unexpected"}},
		} {
			t.Run(test.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"object":"list","data":[],"has_more":false}`)
				}))
				t.Cleanup(server.Close)
				output, err := run(t, server.URL, "raw", "", test.admin, test.args...)
				if err == nil {
					t.Errorf("%s = success (%s), want rejection", test.name, output)
				}
				if got := requests.Load(); got != 0 {
					t.Errorf("%s request count = %d, want 0", test.name, got)
				}
			})
		}
	})
}
