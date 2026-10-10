package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
)

type keyInventoryResource struct {
	name, path, object, idFlag, heading string
	routes                              [][]string
	project                             bool
}

var keyInventoryResources = []keyInventoryResource{
	{
		name: "admin keys", path: "/organization/admin_api_keys", object: "organization.admin_api_key", idFlag: "key-id", heading: "API key · organization Admin-key endpoint",
		routes: [][]string{{"admin:organization:admin-api-keys"}, {"admin", "organization", "admin-api-keys"}, {"admin", "admin-api-keys"}},
	},
	{
		name: "project keys", path: "/organization/projects/proj_inventory/api_keys", object: "organization.project.api_key", idFlag: "api-key-id", project: true, heading: "Project API key · proj_inventory",
		routes: [][]string{{"admin:organization:projects:api-keys"}, {"admin", "organization", "projects", "api-keys"}, {"admin", "projects", "api-keys"}, {"projects", "api-keys"}},
	},
	{
		name: "service accounts", path: "/organization/projects/proj_inventory/service_accounts", object: "organization.project.service_account", idFlag: "service-account-id", project: true, heading: "Service account · proj_inventory",
		routes: [][]string{{"admin:organization:projects:service-accounts"}, {"admin", "organization", "projects", "service-accounts"}, {"admin", "projects", "service-accounts"}, {"projects", "service-accounts"}},
	},
}

func (resource keyInventoryResource) command(route []string, operation string) []string {
	args := append(slices.Clone(route), operation)
	if resource.project {
		args = append(args, "--project-id", "proj_inventory")
	}
	if operation == "retrieve" {
		args = append(args, "--"+resource.idFlag, "inventory_001")
	}
	return args
}

func keyInventoryRecord(resource keyInventoryResource, id string) string {
	return fmt.Sprintf(`{"object":%q,"id":%q,"name":"Synthetic inventory","created_at":1700000000,"expires_at":null,"last_used_at":0,"owner_project_access":"inactive","owner":{"type":"user","user":{"id":"user_inventory","name":"Synthetic owner"}},"future_metadata":{"note":"preserve-future-field"},"value":"synthetic-inventory-secret","api_key":{"id":"key_nested","value":"synthetic-nested-secret"}}`, resource.object, id)
}

func keyInventoryPage(record, last string, more bool) string {
	return fmt.Sprintf(`{"object":"list","data":[%s],"has_more":%t,"first_id":%q,"last_id":%q}`, record, more, last, last)
}

func keyInventoryEnv(server *httptest.Server) []string {
	return []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-inventory-api-key", "OPENAI_ADMIN_KEY=synthetic-inventory-admin-key", "FORCE_COLOR=0"}
}

func TestMainKeyInventoryInvalidResponseRecovery(t *testing.T) {
	const guidance = "The API returned an invalid key inventory response.\nRetry this read command."
	for _, resource := range keyInventoryResources {
		for _, shape := range []string{"malformed", "nonobject"} {
			for _, errorFormat := range []string{"text", "json"} {
				t.Run(resource.name+"/"+shape+"/"+errorFormat, func(t *testing.T) {
					record := keyInventoryRecord(resource, "inventory_001")
					invalid := record[:len(record)-1]
					if shape == "nonobject" {
						invalid = "[" + record + "]"
					}
					var state struct {
						sync.Mutex
						healthy bool
						calls   int
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet || r.URL.Path != resource.path+"/inventory_001" {
							t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						}
						state.Lock()
						healthy := state.healthy
						state.calls++
						state.Unlock()
						w.Header().Set("Content-Type", "application/json")
						body := invalid
						if healthy {
							body = record
						}
						if _, err := io.WriteString(w, body); err != nil {
							t.Errorf("fixture response: %v", err)
						}
					}))
					t.Cleanup(server.Close)
					args := append([]string{"openai"}, resource.command(resource.routes[len(resource.routes)-1], "retrieve")...)
					args = append(args, "--format-error", errorFormat)
					got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
					if got.code != 1 || got.stdout != "" {
						t.Fatalf("invalid inventory did not fail privately: %+v", got)
					}
					if errorFormat == "json" {
						var diagnostic map[string]string
						if err := json.Unmarshal([]byte(got.stderr), &diagnostic); err != nil || !reflect.DeepEqual(diagnostic, map[string]string{"message": guidance}) {
							t.Fatalf("unsafe structured diagnostic: %q, error %v", got.stderr, err)
						}
					} else if got.stderr != guidance+"\n" {
						t.Fatalf("wrong recovery diagnostic: %q", got.stderr)
					}
					for _, marker := range []string{"synthetic-inventory-secret", "synthetic-nested-secret", "inventory_001", "\x1b"} {
						if strings.Contains(got.stdout+got.stderr, marker) {
							t.Fatalf("diagnostic disclosed response marker %q", marker)
						}
					}
					// Follow the recovery instruction without changing the command or context.
					state.Lock()
					state.healthy = true
					state.Unlock()
					recovered := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
					if recovered.code != 0 || recovered.stderr != "" || !strings.Contains(recovered.stdout, "inventory_001") {
						t.Fatalf("retry did not recover: %+v", recovered)
					}
					state.Lock()
					calls := state.calls
					state.Unlock()
					if calls != 2 {
						t.Fatalf("got %d requests, want one failed read and one retry", calls)
					}
				})
			}
		}
	}
}

func TestMainKeyInventoryRoutesPreserveRequestConfiguration(t *testing.T) {
	for _, resource := range keyInventoryResources {
		for _, route := range resource.routes {
			for _, operation := range []string{"list", "retrieve"} {
				t.Run(strings.Join(route, "/")+"/"+operation, func(t *testing.T) {
					record := keyInventoryRecord(resource, "inventory_001")
					response, path := record, resource.path+"/inventory_001"
					if operation == "list" {
						response, path = keyInventoryPage(record, "inventory_001", false), resource.path
					}
					server, requests := globalFlagsServer(t, response)
					args := []string{"openai", "--base-url", server.URL, "--api-key", "synthetic-explicit-api", "--admin-api-key", "synthetic-explicit-admin",
						"--organization", "org_explicit", "--project", "proj_header", "--header", "X-Inventory-Test: first"}
					args = append(args, resource.command(route, operation)...)
					args = append(args, "--header", "X-Inventory-Test: last", "--header", "OpenAI-Project: proj_header_override")
					env := append(keyInventoryEnv(server), "OPENAI_BASE_URL=http://127.0.0.1:1", "OPENAI_ORG_ID=org_env", "OPENAI_PROJECT_ID=proj_env")
					got := runMainDispatchWithEnv(t, "bash", env, args...)
					if got.code != 0 || got.stderr != "" {
						t.Fatalf("inventory failed: %+v", got)
					}
					request := globalFlagsOneRequest(t, requests)
					if request.method != http.MethodGet || request.path != path || len(request.body) != 0 {
						t.Fatalf("inventory request changed: %+v", request)
					}
					for header, want := range map[string]string{"Authorization": "Bearer synthetic-explicit-admin", "OpenAI-Organization": "org_explicit", "OpenAI-Project": "proj_header_override", "X-Inventory-Test": "last"} {
						if actual := request.header.Values(header); !slices.Equal(actual, []string{want}) {
							t.Errorf("%s = %q; want %q", header, actual, want)
						}
					}
					for _, marker := range []string{resource.heading, "inventory_001", "Synthetic inventory", "1700000000", "null", "preserve-future-field", "Owner project access: inactive", "Last used at: 0"} {
						if !strings.Contains(got.stdout, marker) {
							t.Errorf("missing inventory metadata %q: %q", marker, got.stdout)
						}
					}
					for _, secret := range []string{"synthetic-inventory-secret", "synthetic-nested-secret"} {
						if strings.Contains(got.stdout+got.stderr, secret) {
							t.Errorf("readable inventory exposed %q", secret)
						}
					}
					if strings.Contains(got.stdout, "\x1b") || strings.Contains(got.stdout, "Status: inactive") {
						t.Fatalf("inventory introduced controls or inferred key status: %q", got.stdout)
					}
				})
			}
		}
	}
}

func TestMainKeyInventoryExplicitOutputPreservesData(t *testing.T) {
	for _, resource := range keyInventoryResources {
		for _, operation := range []string{"list", "retrieve"} {
			for _, mode := range []string{"json", "jsonl", "yaml", "pretty", "raw", "raw-output", "transform", "raw transform"} {
				t.Run(resource.name+"/"+operation+"/"+mode, func(t *testing.T) {
					record := keyInventoryRecord(resource, "inventory_001")
					response := record
					if operation == "list" {
						response = keyInventoryPage(record, "inventory_001", false)
					}
					server, requests := globalFlagsServer(t, response)
					args := append([]string{"openai"}, resource.command(resource.routes[0], operation)...)
					switch mode {
					case "raw-output":
						args = append(args, "--raw-output")
					case "transform":
						args = append(args, "--transform", "future_metadata.note", "--format", "json")
					case "raw transform":
						args = append(args, "--transform", "value", "--raw-output")
					default:
						args = append(args, "--format", mode)
					}
					got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
					if got.code != 0 || got.stderr != "" || strings.Contains(got.stdout, resource.heading) {
						t.Fatalf("explicit mode intercepted: %+v", got)
					}
					globalFlagsOneRequest(t, requests)
					switch mode {
					case "raw":
						if got.stdout != response+"\n" {
							t.Fatalf("raw bytes changed: %q", got.stdout)
						}
					case "json", "jsonl", "yaml", "raw-output":
						actual := []byte(got.stdout)
						if mode == "yaml" {
							var err error
							actual, err = yaml.YAMLToJSON(actual)
							if err != nil {
								t.Fatal(err)
							}
						}
						assertKeyInventoryJSON(t, actual, []byte(record))
					case "transform":
						if got.stdout != "\"preserve-future-field\"\n" {
							t.Fatalf("extraction changed: %q", got.stdout)
						}
					case "raw transform":
						if got.stdout != "synthetic-inventory-secret\n" {
							t.Fatalf("raw extraction changed: %q", got.stdout)
						}
					default:
						for _, marker := range []string{"synthetic-inventory-secret", "synthetic-nested-secret", "preserve-future-field", "1700000000", "inactive", "null"} {
							if !strings.Contains(got.stdout, marker) {
								t.Errorf("explicit output lost %q: %q", marker, got.stdout)
							}
						}
					}
				})
			}
		}
	}
}

func TestMainKeyInventoryPositionalProjectScope(t *testing.T) {
	for _, resource := range keyInventoryResources[1:] {
		for _, route := range resource.routes {
			for _, operation := range []string{"list", "retrieve"} {
				t.Run(strings.Join(route, "/")+"/"+operation, func(t *testing.T) {
					record := keyInventoryRecord(resource, "inventory_001")
					response, path := record, resource.path+"/inventory_001"
					args := append([]string{"openai"}, route...)
					args = append(args, operation, "proj_inventory")
					if operation == "list" {
						response, path = keyInventoryPage(record, "inventory_001", false), resource.path
					} else {
						args = append(args, "inventory_001")
					}
					server, requests := globalFlagsServer(t, response)
					got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
					if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, resource.heading) {
						t.Fatalf("positional project lost scope: %+v", got)
					}
					if request := globalFlagsOneRequest(t, requests); request.path != path {
						t.Fatalf("positional path=%q; want %q", request.path, path)
					}
				})
			}
		}
	}
}

func assertKeyInventoryJSON(t *testing.T, actual, expected []byte) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatalf("invalid output JSON: %v; %q", err, actual)
	}
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response data changed: got %#v; want %#v", got, want)
	}
}

func TestMainKeyInventoryPreservesMetadataPresence(t *testing.T) {
	for _, resource := range keyInventoryResources {
		for _, metadata := range []struct {
			name, fields, expires, used string
		}{
			{name: "absent"},
			{name: "null", fields: `,"expires_at":null,"last_used_at":null`, expires: "(null)", used: "(null)"},
			{name: "zero", fields: `,"expires_at":0,"last_used_at":0`, expires: "0", used: "0"},
			{name: "returned timestamps", fields: `,"expires_at":1800000000,"last_used_at":1700000000`, expires: "1800000000", used: "1700000000"},
		} {
			t.Run(resource.name+"/"+metadata.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"id":"inventory_001","object":%q,"name":""%s}`, resource.object, metadata.fields)
				server, _ := globalFlagsServer(t, body)
				args := append([]string{"openai", "--format", "text"}, resource.command(resource.routes[0], "retrieve")...)
				got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
				if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, resource.heading) || !strings.Contains(got.stdout, "Name: (empty string)") {
					t.Fatalf("readable metadata changed: %+v", got)
				}
				for label, want := range map[string]string{"Expires at": metadata.expires, "Last used at": metadata.used} {
					if want == "" {
						if strings.Contains(got.stdout, label+":") {
							t.Errorf("absent metadata became a value: %q", got.stdout)
						}
					} else if !strings.Contains(got.stdout, label+": "+want+"\n") {
						t.Errorf("metadata %s lost its original meaning: %q", label, got.stdout)
					}
				}
				for _, inferred := range []string{"Never", "never", "Expired", "expired", "Status:", "Owner:"} {
					if strings.Contains(got.stdout, inferred) {
						t.Errorf("inventory inferred absent metadata %q: %q", inferred, got.stdout)
					}
				}
			})
		}
	}
}

func TestMainKeyInventoryEmptyListsKeepScope(t *testing.T) {
	for _, resource := range keyInventoryResources {
		for _, route := range resource.routes {
			t.Run(strings.Join(route, "/"), func(t *testing.T) {
				server, _ := globalFlagsServer(t, `{"object":"list","data":[],"has_more":false}`)
				args := append([]string{"openai"}, resource.command(route, "list")...)
				got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
				kind, scope, _ := strings.Cut(resource.heading, " · ")
				kind = strings.ReplaceAll(strings.ToLower(kind), "api", "API")
				want := "No " + kind + "s returned for " + scope + ".\n"
				if !resource.project {
					want = "No keys returned by the organization Admin-key endpoint.\n"
				}
				if got.code != 0 || got.stderr != "" || got.stdout != want {
					t.Fatalf("empty inventory lost scope: got %+v; want stdout %q", got, want)
				}
			})
		}
	}
}

func TestMainKeyInventoryCreateResponsesKeepOneTimeSecrets(t *testing.T) {
	for _, create := range []struct {
		name, path, response, secretPath string
		args                             []string
	}{
		{
			name: "admin key", path: "/organization/admin_api_keys", response: `{"id":"key_created","object":"organization.admin_api_key","name":"Synthetic create","value":"synthetic-one-time-secret","expires_at":null}`, secretPath: "value",
			args: []string{"admin", "admin-api-keys", "create", "--name", "Synthetic create"},
		},
		{
			name: "service account", path: "/organization/projects/proj_inventory/service_accounts", response: `{"id":"sa_created","object":"organization.project.service_account","name":"Synthetic create","api_key":{"id":"key_created","value":"synthetic-one-time-secret","expires_at":null}}`, secretPath: "api_key.value",
			args: []string{"projects", "service-accounts", "create", "--project-id", "proj_inventory", "--name", "Synthetic create"},
		},
		{
			name: "service account key", path: "/organization/projects/proj_inventory/service_accounts/sa_created/api_keys", response: `{"id":"key_created","object":"organization.project.service_account.api_key","name":"Synthetic create","value":"synthetic-one-time-secret","expires_at":null}`, secretPath: "value",
			args: []string{"admin:organization:projects:service-accounts:api-keys", "create", "--project-id", "proj_inventory", "--service-account-id", "sa_created", "--name", "Synthetic create"},
		},
	} {
		for _, mode := range []string{"default", "json", "raw", "raw extraction"} {
			t.Run(create.name+"/"+mode, func(t *testing.T) {
				server, requests := globalFlagsServer(t, create.response)
				args := append([]string{"openai", "--header", "X-Inventory-Test: create"}, create.args...)
				if mode == "raw extraction" {
					args = append(args, "--transform", create.secretPath, "--raw-output")
				} else if mode != "default" {
					args = append(args, "--format", mode)
				}
				got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
				if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "synthetic-one-time-secret") || strings.Contains(got.stdout, " · ") || strings.Contains(got.stdout, "[redacted]") {
					t.Fatalf("inventory intercepted creation: %+v", got)
				}
				request := globalFlagsOneRequest(t, requests)
				if request.method != http.MethodPost || request.path != create.path || request.header.Get("X-Inventory-Test") != "create" {
					t.Fatalf("create request changed: %+v", request)
				}
				assertKeyInventoryJSON(t, request.body, []byte(`{"name":"Synthetic create"}`))
				if mode == "json" {
					assertKeyInventoryJSON(t, []byte(got.stdout), []byte(create.response))
				} else if mode == "raw" && got.stdout != create.response+"\n" {
					t.Fatalf("create raw bytes changed: %q", got.stdout)
				} else if mode == "raw extraction" && got.stdout != "synthetic-one-time-secret\n" {
					t.Fatalf("create extraction changed: %q", got.stdout)
				}
			})
		}
	}
}

func TestMainKeyInventoryCreatePreservesBodyInputs(t *testing.T) {
	for _, input := range []struct {
		name, stdin, expected string
		flags                 []string
	}{
		{name: "omitted", expected: `{"name":"Synthetic create"}`},
		{name: "explicit flags", flags: []string{"--expires-in-seconds", "0", "--create-service-account-only=false"}, expected: `{"name":"Synthetic create","expires_in_seconds":0,"create_service_account_only":false}`},
		{name: "null flags", flags: []string{"--expires-in-seconds", "null", "--create-service-account-only=null"}, expected: `{"name":"Synthetic create","expires_in_seconds":null,"create_service_account_only":null}`},
		{name: "JSON body", stdin: `{"expires_in_seconds":1234,"create_service_account_only":false,"future_field":null}`, expected: `{"name":"Synthetic create","expires_in_seconds":1234,"create_service_account_only":false,"future_field":null}`},
		{name: "YAML body", stdin: "expires_in_seconds: null\ncreate_service_account_only: true\nfuture_field: preserved\n", expected: `{"name":"Synthetic create","expires_in_seconds":null,"create_service_account_only":true,"future_field":"preserved"}`},
	} {
		t.Run(input.name, func(t *testing.T) {
			server, requests := globalFlagsServer(t, `{"id":"sa_created","api_key":{"value":"synthetic-one-time-secret"}}`)
			args := []string{"openai", "projects", "service-accounts", "create", "--project-id", "proj_inventory", "--name", "Synthetic create", "--format", "json"}
			args = append(args, input.flags...)
			var stdin *os.File
			if input.stdin != "" {
				stdin = shellFileInput(t, []byte(input.stdin))
			}
			got := runMainDispatchWithStdin(t, "bash", keyInventoryEnv(server), stdin, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "synthetic-one-time-secret") {
				t.Fatalf("create body input failed: %+v", got)
			}
			assertKeyInventoryJSON(t, globalFlagsOneRequest(t, requests).body, []byte(input.expected))
		})
	}
}

func TestMainKeyInventoryPagination(t *testing.T) {
	for _, resource := range keyInventoryResources {
		for _, limit := range []struct {
			max, count int
		}{{-1, 2}, {1, 1}, {0, 0}} {
			t.Run(fmt.Sprintf("%s/max=%d", resource.name, limit.max), func(t *testing.T) {
				var mu sync.Mutex
				var cursors []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					cursor := r.URL.Query().Get("after")
					mu.Lock()
					cursors = append(cursors, cursor)
					mu.Unlock()
					if r.Method != http.MethodGet || r.URL.Path != resource.path || r.URL.Query().Get("limit") != "1" {
						t.Errorf("unexpected pagination request: %s %s", r.Method, r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					if cursor == "" {
						io.WriteString(w, keyInventoryPage(keyInventoryRecord(resource, "inventory_001"), "inventory_001", true))
					} else if cursor == "inventory_001" {
						io.WriteString(w, keyInventoryPage(keyInventoryRecord(resource, "inventory_002"), "inventory_002", false))
					} else {
						http.Error(w, "unexpected cursor", http.StatusBadRequest)
					}
				}))
				t.Cleanup(server.Close)
				args := append([]string{"openai"}, resource.command(resource.routes[len(resource.routes)-1], "list")...)
				args = append(args, "--limit", "1", "--max-items", fmt.Sprint(limit.max))
				got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("pagination failed: %+v", got)
				}
				mu.Lock()
				actual := slices.Clone(cursors)
				mu.Unlock()
				want := []string{"", "inventory_001"}[:max(1, limit.count)]
				if !slices.Equal(actual, want) {
					t.Fatalf("cursors=%q; want %q", actual, want)
				}
				for number := 1; number <= 2; number++ {
					want := 0
					if number <= limit.count {
						want = 1
					}
					if count := strings.Count(got.stdout, fmt.Sprintf("inventory_%03d", number)); count != want {
						t.Errorf("item %d appears %d times; want %d: %q", number, count, want, got.stdout)
					}
				}
			})
		}
	}
}

func TestMainKeyInventoryProjectOwnerAccessFilter(t *testing.T) {
	resource := keyInventoryResources[1]
	for _, filter := range []string{"", "active", "inactive", "any"} {
		t.Run("filter="+filter, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if actual := r.URL.Query().Get("owner_project_access"); actual != filter {
					t.Errorf("owner access filter=%q; want %q", actual, filter)
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-header-admin" {
					t.Error("custom Authorization header lost precedence")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, keyInventoryPage(keyInventoryRecord(resource, "inventory_001"), "inventory_001", false))
			}))
			t.Cleanup(server.Close)
			args := append([]string{"openai", "--header", "Authorization: Bearer synthetic-header-admin"}, resource.command(resource.routes[len(resource.routes)-1], "list")...)
			if filter != "" {
				args = append(args, "--owner-project-access", filter)
			}
			got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Owner project access: inactive") || strings.Contains(got.stdout, "Status:") {
				t.Fatalf("owner access became key status: %+v", got)
			}
		})
	}
}

func TestMainKeyInventoryErrorsPreservePartialOutput(t *testing.T) {
	for _, resource := range keyInventoryResources {
		for _, partial := range []bool{false, true} {
			for _, body := range []string{`{"error":{"message":"synthetic inventory failure","type":"invalid_request_error"}}`, `{}`} {
				t.Run(fmt.Sprintf("%s/partial=%t/body=%s", resource.name, partial, body), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if partial && r.URL.Query().Get("after") == "" {
							io.WriteString(w, keyInventoryPage(keyInventoryRecord(resource, "inventory_001"), "inventory_001", true))
							return
						}
						w.WriteHeader(http.StatusForbidden)
						io.WriteString(w, body)
					}))
					t.Cleanup(server.Close)
					args := append([]string{"openai"}, resource.command(resource.routes[0], "list")...)
					got := runMainDispatchWithEnv(t, "bash", keyInventoryEnv(server), args...)
					if got.code != 1 || got.stderr == "" || strings.Contains(got.stdout, "No results.") || strings.Contains(got.stdout, "No admin API keys") {
						t.Fatalf("error became success or empty inventory: %+v", got)
					}
					if strings.Contains(got.stdout, "inventory_001") != partial {
						t.Fatalf("partial inventory changed: %+v", got)
					}
				})
			}
		}
	}
}

func TestMainKeyInventoryInterruptClosesPagination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal does not send os.Interrupt on Windows")
	}
	resource := keyInventoryResources[1]
	ready, disconnected := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, keyInventoryPage(keyInventoryRecord(resource, "inventory_001"), "inventory_001", true))
			return
		}
		close(ready)
		<-r.Context().Done()
		close(disconnected)
	}))
	t.Cleanup(server.Close)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, resource.command(resource.routes[0], "list")...)
	child := exec.CommandContext(ctx, binary, args...)
	child.Env = append(keyInventoryEnv(server), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		waited = true
		t.Fatalf("inventory exited before interrupt: %v; stderr=%q", err, stderr.String())
	case <-ctx.Done():
		t.Fatal("inventory did not start its second request")
	}
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err = <-done
	waited = true
	if ctx.Err() != nil || err == nil || !strings.Contains(stdout.String(), "inventory_001") {
		t.Fatalf("interrupt lost partial output or failed to exit: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("inventory left the interrupted HTTP request open")
	}
}
