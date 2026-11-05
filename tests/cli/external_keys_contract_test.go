package cli_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
)

func TestExternalKeysPaginationContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	mocktest.CopyCLI(t, binary)

	for _, test := range []struct {
		name                 string
		maxItems             string
		wantItems, wantPages int
		secondHasMore        bool
	}{
		{"all pages", "-1", 4, 2, false},
		{"limit within page", "3", 3, 2, true},
		{"limit at page boundary", "2", 2, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := int(requests.Add(1))
				w.Header().Set("Content-Type", "application/json")
				if page > test.wantPages {
					t.Errorf("unexpected page request %d; wanted %d", page, test.wantPages)
					fmt.Fprint(w, `{"object":"list","data":[],"has_more":false}`)
					return
				}
				after := "key_start"
				if page == 2 {
					after = "key_2"
				}
				wantQuery := url.Values{
					"after": {after}, "limit": {"2"}, "order": {"asc"}, "purpose": {"psp_decryption"},
				}
				if r.Method != http.MethodGet || r.URL.Path != "/organization/external_keys" || !reflect.DeepEqual(r.URL.Query(), wantQuery) {
					t.Errorf("page %d request = %s %s; want GET /organization/external_keys with %v", page, r.Method, r.URL.RequestURI(), wantQuery)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer synthetic-admin-key" {
					t.Errorf("Authorization = %q; want synthetic admin bearer", got)
				}
				items := make([]map[string]any, 0, 2)
				for i := 2*page - 1; i <= 2*page; i++ {
					items = append(items, map[string]any{
						"id": fmt.Sprintf("key_%d", i), "object": "organization.external_key",
						"type": "aws", "name": "Synthetic key", "created_at": 1,
						"workspace_id": "org_test", "api_project_ids": []string{},
					})
				}
				if err := json.NewEncoder(w).Encode(map[string]any{
					"object": "list", "data": items, "has_more": page == 1 || test.secondHasMore,
					"first_id": items[0]["id"], "last_id": items[1]["id"],
				}); err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			command := exec.CommandContext(t.Context(), binary,
				"--base-url", server.URL, "--format", "jsonl", "admin:organization:external-keys", "list",
				"--after", "key_start", "--limit", "2", "--order", "asc", "--purpose", "psp_decryption",
				"--max-items", test.maxItems,
			)
			// Only synthetic credentials; do not inherit local proxies or mTLS configuration.
			command.Env = []string{"FORCE_COLOR=0", "OPENAI_API_KEY=synthetic-project-key", "OPENAI_ADMIN_KEY=synthetic-admin-key"}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("list failed: %v\n%s", err, output)
			}
			if got := int(requests.Load()); got != test.wantPages {
				t.Errorf("request count = %d; want %d", got, test.wantPages)
			}
			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			if len(lines) != test.wantItems {
				t.Fatalf("output = %s; want %d items", output, test.wantItems)
			}
			for i, line := range lines {
				var item struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal([]byte(line), &item); err != nil {
					t.Fatalf("decode item %d: %v", i, err)
				}
				if want := fmt.Sprintf("key_%d", i+1); item.ID != want {
					t.Errorf("item %d ID = %q; want %q", i, item.ID, want)
				}
			}
		})
	}
}

func TestExternalKeysProviderContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	mocktest.CopyCLI(t, binary)
	providers := map[string]map[string]string{
		"aws":   {"external_id": "org_synthetic", "kms_arn": "arn:aws:kms:us-east-1:123456789012:key/synthetic", "role_arn": "arn:aws:iam::123456789012:role/example-role"},
		"gcp":   {"audience": "org_synthetic", "kms_key_location": "global", "kms_key_name": "synthetic-key", "kms_key_ring_name": "synthetic-ring", "kms_project_id": "synthetic-project", "workload_identity_pool_id": "synthetic-pool", "workload_identity_project_number": "123456789012", "workload_identity_provider_id": "synthetic-provider", "kms_key_version": "7"},
		"azure": {"key_name": "synthetic-key", "tenant_id": "00000000-0000-4000-8000-000000000001", "vault_uri": "https://synthetic.vault.azure.net", "key_version": "7"},
	}
	for _, provider := range []string{"aws", "gcp", "azure"} {
		want := maps.Clone(providers[provider])
		want["type"], want["name"], want["purpose"] = provider, "synthetic-example", "psp_decryption"
		modes := []string{"flags", "piped-json", "explicit-type-override", "missing-provider-flags", "missing-provider-piped", "missing-name-flags", "missing-name-piped", "ekm-flags", "ekm-piped", "file-type-flags", "missing-field/" + slices.Sorted(maps.Keys(providers[provider]))[0] + "/override"}
		for _, field := range slices.Sorted(maps.Keys(providers[provider])) {
			if field != "kms_key_version" && field != "key_version" {
				modes = append(modes, "missing-field/"+field+"/flags", "missing-field/"+field+"/piped")
			}
		}
		for _, mode := range modes {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				want := maps.Clone(want)
				if strings.HasPrefix(mode, "ekm-") {
					want["purpose"] = "ekm"
					delete(want, "kms_key_version")
					delete(want, "key_version")
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/organization/external_keys" || r.URL.RawQuery != "" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
					}
					if got := r.Header.Get("Authorization"); got != "Bearer synthetic-admin-key" {
						t.Errorf("Authorization = %q; want synthetic admin bearer", got)
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode request: %v", err)
					}
					if !reflect.DeepEqual(body, want) {
						t.Errorf("request body = %#v; want %#v", body, want)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"key_synthetic","type":%q,"name":"synthetic-example"}`, provider)
				}))
				t.Cleanup(server.Close)
				body := maps.Clone(want)
				if mode == "file-type-flags" {
					path := filepath.Join(t.TempDir(), "provider.txt")
					if err := os.WriteFile(path, []byte(provider), 0600); err != nil {
						t.Fatal(err)
					}
					body["type"] = "@" + path
				}
				missing := ""
				if strings.HasPrefix(mode, "missing-provider") {
					missing = "type"
				}
				if strings.HasPrefix(mode, "missing-name") {
					missing = "name"
				}
				if strings.HasPrefix(mode, "missing-field/") {
					missing = strings.Split(mode, "/")[1]
				}
				delete(body, missing)
				args := []string{"--base-url", server.URL, "admin", "organization", "external-keys", "create"}
				flagMode := strings.HasSuffix(mode, "flags")
				if strings.HasPrefix(mode, "missing-field/") && !flagMode {
					args = []string{"--base-url", server.URL, "admin:organization:external-keys", "create"}
				}
				if flagMode {
					for _, key := range slices.Sorted(maps.Keys(body)) {
						args = append(args, "--"+strings.ReplaceAll(key, "_", "-"), body[key])
					}
				}
				if mode == "explicit-type-override" || strings.HasSuffix(mode, "/override") {
					body["type"] = "aws"
					if provider == "aws" {
						body["type"] = "azure"
					}
					args = append(args, "--type", provider)
				}
				command := exec.CommandContext(t.Context(), binary, args...)
				// Synthetic credentials only; a project key must never replace admin authentication.
				command.Env = []string{"FORCE_COLOR=0", "OPENAI_API_KEY=synthetic-project-key", "OPENAI_ADMIN_KEY=synthetic-admin-key"}
				if !flagMode {
					input, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					command.Stdin = strings.NewReader(string(input))
				}
				output, err := command.CombinedOutput()
				if missing != "" {
					if err == nil || !strings.Contains(strings.ToLower(string(output)), "required") || !strings.Contains(string(output), "--"+strings.ReplaceAll(missing, "_", "-")) {
						t.Fatalf("missing %s was not rejected: %v; %s", missing, err, output)
					}
					if requests.Load() != 0 {
						t.Fatal("invalid request reached HTTP")
					}
					return
				}
				if err != nil {
					t.Fatalf("create failed: %v; %s", err, output)
				}
				if requests.Load() != 1 {
					t.Fatalf("request count = %d; want 1", requests.Load())
				}
			})
		}
	}
}
