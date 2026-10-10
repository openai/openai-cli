package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/goccy/go-yaml"
)

const dataControlsStorage = `{"object":"organization.external_storage","id":"extstorage_returned","project_id":"proj_returned","provider":{"type":"aws","bucket":"synthetic-bucket","role_arn":"arn:aws:iam::123456789012:role/Synthetic","external_id":"synthetic-external","account_id":"123456789012","region":"us-east-1"},"geography":"us","status":"pending","created_at":17}`

// Exercise the normal entrypoint with explicit request configuration overriding
// synthetic environment defaults. The response projection must not alter it.
func runDataControlsCommand(t *testing.T, server *httptest.Server, input *os.File, args ...string) mainDispatchResult {
	t.Helper()
	home := t.TempDir()
	env := []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_API_KEY=synthetic-api-env", "OPENAI_ADMIN_KEY=synthetic-admin-env",
		"OPENAI_ORG_ID=org_env", "OPENAI_PROJECT_ID=proj_env", "FORCE_COLOR=0",
		"OPENAI_CUSTOM_HEADERS=X-Data-Controls: environment",
	}
	argv := []string{"openai", "--base-url", server.URL + "/v1/", "--api-key", "synthetic-api-explicit",
		"--admin-api-key", "synthetic-admin-explicit", "--organization", "org_explicit", "--project", "proj_context"}
	argv = append(argv, args...)
	argv = append(argv, "--header", "X-Data-Controls: explicit")
	return runMainDispatchWithStdin(t, "bash", env, input, argv...)
}

func checkDataControlsRequest(t *testing.T, r *http.Request, method, path, body string) {
	t.Helper()
	if r.Method != method || r.URL.Path != "/v1"+path {
		t.Errorf("request = %s %s, want %s /v1%s", r.Method, r.URL, method, path)
	}
	for name, want := range map[string]string{
		"Authorization": "Bearer synthetic-admin-explicit", "OpenAI-Organization": "org_explicit",
		"OpenAI-Project": "proj_context", "X-Data-Controls": "explicit",
	} {
		if got := r.Header.Get(name); got != want {
			t.Errorf("%s changed: got %q, want %q", name, got, want)
		}
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
		return
	}
	if body == "" {
		if len(data) != 0 {
			t.Errorf("unexpected request body: %q", data)
		}
		return
	}
	if got, want := decodeDataControlsJSON(t, string(data)), decodeDataControlsJSON(t, body); !reflect.DeepEqual(got, want) {
		t.Errorf("request body changed: got %#v, want %#v", got, want)
	}
}

func decodeDataControlsJSON(t *testing.T, text string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("invalid JSON: %v; %q", err, text)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("unexpected trailing JSON: %v; %q", err, text)
	}
	return value
}

func TestMainDataControlsRetentionRoutes(t *testing.T) {
	for _, route := range []struct {
		args    []string
		project bool
	}{
		{[]string{"admin:organization:data-retention"}, false},
		{[]string{"admin", "organization", "data-retention"}, false},
		{[]string{"admin", "data-retention"}, false},
		{[]string{"admin:organization:projects:data-retention"}, true},
		{[]string{"admin", "organization", "projects", "data-retention"}, true},
		{[]string{"admin", "projects", "data-retention"}, true},
		{[]string{"projects", "data-retention"}, true},
	} {
		for _, operation := range []string{"retrieve", "update"} {
			t.Run(strings.Join(route.args, "/")+"/"+operation, func(t *testing.T) {
				path, object, setting := "/organization/data_retention", "organization.data_retention", "enhanced_zero_data_retention"
				args := append(append([]string{}, route.args...), operation)
				if route.project {
					path, object, setting = "/organization/projects/proj_target/data_retention", "project.data_retention", "organization_default"
					args = append(args, "--project-id", "proj_target")
				}
				method, body := http.MethodGet, ""
				if operation == "update" {
					method, body = http.MethodPost, fmt.Sprintf(`{"retention_type":%q}`, setting)
					args = append(args, "--retention-type", setting)
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					checkDataControlsRequest(t, r, method, path, body)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"object":%q,"type":%q}`, object, setting)
				}))
				defer server.Close()
				got := runDataControlsCommand(t, server, nil, args...)
				if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
					t.Fatalf("result=%+v requests=%d", got, requests.Load())
				}
				configured := "Configured retention: " + setting
				if route.project {
					configured = "Configured retention: inherit organization default (organization_default)"
				}
				if !strings.Contains(got.stdout, configured+"\n") || !strings.Contains(got.stdout, "Effective retention: not resolved by this response\n") {
					t.Fatalf("retention configuration or uncertainty missing: %q", got.stdout)
				}
			})
		}
	}
}

func TestMainDataControlsStorageProviders(t *testing.T) {
	for _, tc := range []struct{ provider, input string }{
		{`{"type":"aws","bucket":"synthetic-bucket","role_arn":"arn:aws:iam::123456789012:role/Synthetic"}`, "flags"},
		{`{"type":"azure","tenant_id":"synthetic-tenant","subscription_id":"synthetic-subscription","resource_group":"synthetic-group","account_name":"syntheticaccount","container":"synthetic-container"}`, "stdin"},
		{`{"type":"gcp","bucket":"synthetic-bucket","workload_identity_project_number":"123456789012","workload_identity_pool_id":"synthetic-pool","workload_identity_provider_id":"synthetic-provider"}`, "file"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			body := `{"project_id":"proj_target","provider":` + tc.provider + `}`
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				checkDataControlsRequest(t, r, http.MethodPost, "/organization/external_storage", body)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"object":"organization.external_storage","id":"extstorage_returned","project_id":"proj_target","provider":%s,"status":"pending","created_at":17,"geography":"us"}`, tc.provider)
			}))
			defer server.Close()
			args := []string{"admin", "external-storage", "create"}
			var input *os.File
			switch tc.input {
			case "stdin":
				input = shellFileInput(t, []byte(body))
			case "file":
				name := filepath.Join(t.TempDir(), "provider.json")
				if err := os.WriteFile(name, []byte(tc.provider), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--project-id", "proj_target", "--provider", "@"+name)
			default:
				args = append(args, "--project-id", "proj_target", "--provider", tc.provider)
			}
			got := runDataControlsCommand(t, server, input, args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 || !strings.Contains(got.stdout, "ID: extstorage_returned\n") || !strings.Contains(got.stdout, "Validation status: pending\n") {
				t.Fatalf("provider create result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainDataControlsStorageLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, response string
		args, want                   []string
	}{
		{"retrieve", http.MethodGet, "/organization/external_storage/extstorage_requested", dataControlsStorage,
			[]string{"admin:organization:external-storage", "retrieve", "extstorage_requested"}, []string{"ID: extstorage_returned", "Project ID: proj_returned", "Validation status: pending", "Validation note:"}},
		{"validate pending", http.MethodPost, "/organization/external_storage/extstorage_requested/validate", dataControlsStorage,
			[]string{"admin", "external-storage", "validate", "--external-storage-id", "extstorage_requested"}, []string{"Validation status: pending", "Validation note:", "not complete"}},
		{"validate validated", http.MethodPost, "/organization/external_storage/extstorage_requested/validate", strings.Replace(dataControlsStorage, `"pending"`, `"validated"`, 1),
			[]string{"admin", "organization", "external-storage", "validate", "extstorage_requested"}, []string{"Validation status: validated", "Validation note:", "does not establish continuous storage health"}},
		{"validate unhealthy", http.MethodPost, "/organization/external_storage/extstorage_requested/validate", strings.Replace(dataControlsStorage, `"pending"`, `"unhealthy"`, 1),
			[]string{"admin:organization:external-storage", "validate", "extstorage_requested"}, []string{"Validation status: unhealthy", "Validation note:"}},
		{"delete", http.MethodDelete, "/organization/external_storage/extstorage_requested", `{"object":"organization.external_storage.deleted","id":"extstorage_returned","deleted":true}`,
			[]string{"admin", "external-storage", "delete", "extstorage_requested"}, []string{"ID: extstorage_returned", "Deleted: true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				checkDataControlsRequest(t, r, tc.method, tc.path, "")
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.response)
			}))
			defer server.Close()
			got := runDataControlsCommand(t, server, nil, tc.args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
			for _, want := range tc.want {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("missing %q in %q", want, got.stdout)
				}
			}
			if strings.Contains(got.stdout, "extstorage_requested") || strings.Contains(got.stdout, "Validation status: success") {
				t.Errorf("response invented an ID or status: %q", got.stdout)
			}
		})
	}
}

func TestMainDataControlsStorageListPreservesRecords(t *testing.T) {
	const page = `{"object":"list","data":[` + dataControlsStorage + `,` + dataControlsStorage + `],"has_more":false,"last_id":"extstorage_returned"}`
	for _, format := range []string{"text", "raw", "extraction"} {
		t.Run(format, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				checkDataControlsRequest(t, r, http.MethodGet, "/organization/external_storage", "")
				if r.URL.Query().Get("project_id") != "proj_filter" {
					t.Errorf("project filter changed: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, page)
			}))
			defer server.Close()
			args := []string{"admin", "external-storage", "list", "--project-id", "proj_filter", "--max-items", "-1"}
			if format == "extraction" {
				args = append(args, "--transform", "id", "--raw-output")
			} else {
				args = append(args, "--format", format)
			}
			got := runDataControlsCommand(t, server, nil, args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("list result=%+v requests=%d", got, requests.Load())
			}
			switch format {
			case "raw":
				if got.stdout != page+"\n" {
					t.Fatalf("raw list page changed: %q", got.stdout)
				}
			case "extraction":
				if got.stdout != "extstorage_returned\nextstorage_returned\n" {
					t.Fatalf("list extraction changed: %q", got.stdout)
				}
			default:
				if strings.Count(got.stdout, "ID: extstorage_returned\n") != 2 || strings.Count(got.stdout, "Validation status: pending\n") != 2 {
					t.Fatalf("list lost records or status: %q", got.stdout)
				}
			}
		})
	}
}

func TestMainDataControlsStorageListFailure(t *testing.T) {
	for _, failAfterFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(failAfterFirst), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				checkDataControlsRequest(t, r, http.MethodGet, "/organization/external_storage", "")
				query := r.URL.Query()
				if query.Get("project_id") != "proj_filter" || query.Get("limit") != "1" || query.Get("order") != "asc" {
					t.Errorf("list filters changed: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if query.Get("after") == "extstorage_before" && failAfterFirst {
					fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":true,"last_id":"extstorage_returned"}`, dataControlsStorage)
					return
				}
				wantAfter := "extstorage_before"
				if failAfterFirst {
					wantAfter = "extstorage_returned"
				}
				if query.Get("after") != wantAfter {
					t.Errorf("pagination cursor=%q, want %q", query.Get("after"), wantAfter)
				}
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":{"message":"synthetic storage permission denied","type":"invalid_request_error","code":"permission_denied"}}`)
			}))
			defer server.Close()
			got := runDataControlsCommand(t, server, nil, "admin", "external-storage", "list", "--project-id", "proj_filter", "--after", "extstorage_before", "--limit", "1", "--order", "asc", "--max-items", "-1", "--format-error", "json")
			wantRequests := int32(1)
			if failAfterFirst {
				wantRequests = 2
			}
			if got.code == 0 || requests.Load() != wantRequests || !strings.Contains(got.stderr, "synthetic storage permission denied") || strings.Contains(got.stdout, "No results.") {
				t.Fatalf("list error hidden: result=%+v requests=%d", got, requests.Load())
			}
			if failAfterFirst != strings.Contains(got.stdout, "ID: extstorage_returned\n") {
				t.Fatalf("partial list output changed: %q", got.stdout)
			}
		})
	}
}

func TestMainDataControlsValidationErrors(t *testing.T) {
	const details = `{"message":"synthetic validation forbidden","type":"invalid_request_error","code":"permission_denied"}`
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				checkDataControlsRequest(t, r, http.MethodPost, "/organization/external_storage/extstorage_requested/validate", "")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":`+details+`}`)
			}))
			defer server.Close()
			got := runDataControlsCommand(t, server, nil, "admin", "external-storage", "validate", "extstorage_requested", "--format", format)
			if got.code == 0 || got.stdout != "" || requests.Load() != 1 {
				t.Fatalf("validation error changed: result=%+v requests=%d", got, requests.Load())
			}
			if format == "json" && !reflect.DeepEqual(decodeDataControlsJSON(t, got.stderr), decodeDataControlsJSON(t, details)) {
				t.Fatalf("structured error changed: %q", got.stderr)
			}
			if format == "text" && (!strings.Contains(got.stderr, "Check your key's permissions.") || strings.Contains(got.stderr, "synthetic validation forbidden")) {
				t.Fatalf("readable permission guidance changed: %q", got.stderr)
			}
			if strings.Contains(got.stderr, "Validation note:") || strings.Contains(got.stderr, "Validation status:") {
				t.Fatalf("success projection entered error output: %q", got.stderr)
			}
		})
	}
}

func TestMainDataControlsHelpAcrossRoutes(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
	}{
		{[]string{"admin:organization:data-retention", "retrieve"}, []string{"configured retention", "does not resolve effective retention"}},
		{[]string{"admin", "data-retention", "update"}, []string{"configured retention", "does not resolve effective retention"}},
		{[]string{"admin:organization:projects:data-retention", "retrieve"}, []string{"configured retention", "does not resolve effective retention"}},
		{[]string{"admin", "organization", "projects", "data-retention", "update"}, []string{"configured retention", "does not resolve effective retention"}},
		{[]string{"admin", "projects", "data-retention", "retrieve"}, []string{"configured retention", "does not resolve effective retention"}},
		{[]string{"projects", "data-retention", "update"}, []string{"configured retention", "does not resolve effective retention"}},
		{[]string{"admin:organization:external-storage", "validate"}, []string{"writes cloud test objects", "activates customer-managed retention", "pending response"}},
		{[]string{"admin", "organization", "external-storage", "validate"}, []string{"writes cloud test objects", "activates customer-managed retention", "pending response"}},
		{[]string{"admin", "external-storage", "validate"}, []string{"writes cloud test objects", "activates customer-managed retention", "pending response"}},
		{[]string{"admin", "external-storage", "create"}, []string{"Registration does not complete validation"}},
		{[]string{"admin", "external-storage", "retrieve"}, []string{"saved validation result", "does not run validation", "does not establish continuous storage health"}},
		{[]string{"admin", "external-storage", "list"}, []string{"saved validation result", "does not run validation", "does not establish continuous storage health"}},
	} {
		t.Run(strings.Join(tc.args, "/"), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			got := runDataControlsCommand(t, server, nil, append(tc.args, "--help")...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 0 {
				t.Fatalf("help result=%+v requests=%d", got, requests.Load())
			}
			text := strings.Join(strings.Fields(got.stdout), " ")
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("help lost %q: %q", want, text)
				}
			}
		})
	}
}

func TestMainDataControlsReadCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send os.Interrupt on Windows")
	}
	for _, args := range [][]string{
		{"projects", "data-retention", "retrieve", "proj_target"},
		{"admin", "external-storage", "retrieve", "extstorage_requested"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			started, disconnected := make(chan struct{}, 1), make(chan struct{}, 1)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("inspection sent %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				started <- struct{}{}
				<-r.Context().Done()
				disconnected <- struct{}{}
			}))
			t.Cleanup(server.Close)
			child, stdout, stderr, ctx := startStreamingTextCommand(t, server, append([]string{"--admin-api-key", "synthetic-admin-cancel"}, args...)...)
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("inspection did not reach the controlled server")
			}
			if err := child.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			output, readErr := io.ReadAll(stdout)
			err := child.Wait()
			if ctx.Err() != nil || err == nil || readErr != nil || len(output) != 0 || requests.Load() != 1 {
				t.Fatalf("cancellation lost: exit=%v read=%v stdout=%q stderr=%q requests=%d", err, readErr, output, stderr.String(), requests.Load())
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("cancelled inspection left its HTTP response open")
			}
		})
	}
}

func TestMainDataControlsFormatsAndExtraction(t *testing.T) {
	for _, tc := range []struct {
		name, body, field, value string
		args                     []string
	}{
		{"retention", `{"object":"project.data_retention","type":"organization_default","future":{"count":9007199254740993,"enabled":false,"value":null}}`, "type", "organization_default", []string{"projects", "data-retention", "retrieve", "proj_target"}},
		{"storage", dataControlsStorage, "status", "pending", []string{"admin", "external-storage", "retrieve", "extstorage_requested"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []string{"json", "jsonl", "raw", "yaml", "extraction"} {
				t.Run(format, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, tc.body)
					}))
					defer server.Close()
					args := append([]string{}, tc.args...)
					if format == "extraction" {
						args = append(args, "--transform", tc.field, "--raw-output")
					} else {
						args = append(args, "--format", format)
					}
					got := runDataControlsCommand(t, server, nil, args...)
					if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
						t.Fatalf("result=%+v requests=%d", got, requests.Load())
					}
					if format == "extraction" {
						if got.stdout != tc.value+"\n" {
							t.Fatalf("extraction changed: %q", got.stdout)
						}
						return
					}
					data := []byte(got.stdout)
					if format == "yaml" {
						var err error
						data, err = yaml.YAMLToJSON(data)
						if err != nil {
							t.Fatal(err)
						}
					}
					if got, want := decodeDataControlsJSON(t, string(data)), decodeDataControlsJSON(t, tc.body); !reflect.DeepEqual(got, want) {
						t.Fatalf("API fields changed: got %#v, want %#v", got, want)
					}
					if format == "raw" && got.stdout != tc.body+"\n" || format == "jsonl" && strings.Count(got.stdout, "\n") != 1 {
						t.Fatalf("%s bytes changed: %q", format, got.stdout)
					}
				})
			}
		})
	}
}

func TestMainDataControlsUnknownAndAbsentValues(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		args, want []string
		absent     bool
	}{
		{"unknown retention", `{"object":"project.data_retention","type":"future_retention","future":{"enabled":false,"value":null}}`, []string{"projects", "data-retention", "retrieve", "proj_target"}, []string{"future_retention", "Enabled: false", "Value: (null)"}, false},
		{"absent retention", `{"object":"project.data_retention"}`, []string{"projects", "data-retention", "retrieve", "proj_target"}, []string{"Object: project.data_retention"}, true},
		{"null retention", `{"object":"project.data_retention","type":null}`, []string{"projects", "data-retention", "retrieve", "proj_target"}, []string{"Type: (null)"}, true},
		{"unknown storage", `{"object":"organization.external_storage","id":"extstorage_returned","status":"future_status","failure_details":{"reason":"synthetic failure"}}`, []string{"admin", "external-storage", "retrieve", "extstorage_requested"}, []string{"future_status", "Reason: synthetic failure"}, false},
		{"absent storage", `{"object":"organization.external_storage","id":"extstorage_returned"}`, []string{"admin", "external-storage", "retrieve", "extstorage_requested"}, []string{"ID: extstorage_returned"}, true},
		{"null storage", `{"object":"organization.external_storage","id":"extstorage_returned","status":null}`, []string{"admin", "external-storage", "retrieve", "extstorage_requested"}, []string{"Status: (null)"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got := runDataControlsCommand(t, server, nil, tc.args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
			for _, want := range tc.want {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("lost %q in %q", want, got.stdout)
				}
			}
			if tc.absent && (strings.Contains(got.stdout, "Configured retention:") || strings.Contains(got.stdout, "Validation status:")) {
				t.Errorf("invented a value for absent or null API data: %q", got.stdout)
			}
		})
	}
}
