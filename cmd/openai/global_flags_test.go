package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const globalFlagsModel = `{"id":"model_synthetic","object":"model","created":17,"owned_by":"synthetic"}`
const globalFlagsPage = `{"object":"list","data":[` + globalFlagsModel + `]}`

type globalFlagsRequest struct {
	method, path string
	header       http.Header
	body         []byte
}

func globalFlagsServer(t *testing.T, response string) (*httptest.Server, <-chan globalFlagsRequest) {
	t.Helper()
	requests := make(chan globalFlagsRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requests <- globalFlagsRequest{r.Method, r.URL.Path, r.Header.Clone(), body}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func globalFlagsEnv(server *httptest.Server, extra ...string) []string {
	return append([]string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-env-key", "FORCE_COLOR=0"}, extra...)
}

func globalFlagsOneRequest(t *testing.T, requests <-chan globalFlagsRequest) globalFlagsRequest {
	t.Helper()
	select {
	case request := <-requests:
		if len(requests) != 0 {
			t.Fatalf("unexpected additional requests: %d", len(requests))
		}
		return request
	default:
		t.Fatal("command made no request")
		return globalFlagsRequest{}
	}
}

// Insert only between command words. Flag values remain untouched, including
// empty values and strings that look like flags.
func globalFlagsAt(command, flags []string, position int) []string {
	args := append([]string{"openai"}, command[:position]...)
	args = append(args, flags...)
	return append(args, command[position:]...)
}

func TestMainGlobalFlagsPlacementHeadersAndOutput(t *testing.T) {
	for _, format := range []string{"json", "text", "raw", "extraction"} {
		for _, equals := range []bool{false, true} {
			for position, placement := range []string{"before", "between", "after"} {
				t.Run(format+"/"+map[bool]string{false: "spaced", true: "equals"}[equals]+"/"+placement, func(t *testing.T) {
					server, requests := globalFlagsServer(t, globalFlagsPage)
					flags := []string{}
					add := func(name, value string) {
						if equals {
							flags = append(flags, "--"+name+"="+value)
						} else {
							flags = append(flags, "--"+name, value)
						}
					}
					add("project", "proj-explicit")
					add("organization", "org-explicit")
					add("api-key", "synthetic-explicit-key")
					add("base-url", server.URL)
					if format == "extraction" {
						add("transform", "id")
						flags = append(flags, "-r")
					} else {
						add("format", format)
					}
					got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server,
						"OPENAI_PROJECT_ID=proj-env", "OPENAI_ORG_ID=org-env", "OPENAI_BASE_URL=http://127.0.0.1:1"),
						globalFlagsAt([]string{"models", "list"}, flags, position)...)
					wantStderr := ""
					if format == "text" {
						wantStderr = "Full data: --format json.\n"
					}
					if got.code != 0 || got.stderr != wantStderr {
						t.Fatalf("command failed: %+v", got)
					}
					request := globalFlagsOneRequest(t, requests)
					if request.method != http.MethodGet || request.path != "/models" {
						t.Fatalf("unexpected request: %s %s", request.method, request.path)
					}
					for name, want := range map[string]string{
						"OpenAI-Project": "proj-explicit", "OpenAI-Organization": "org-explicit", "Authorization": "Bearer synthetic-explicit-key",
					} {
						if got := request.header.Values(name); !slices.Equal(got, []string{want}) {
							t.Errorf("%s = %q; want %q", name, got, want)
						}
					}
					switch format {
					case "json":
						var item map[string]any
						if err := json.Unmarshal([]byte(got.stdout), &item); err != nil || item["id"] != "model_synthetic" || item["created"] != float64(17) {
							t.Fatalf("JSON items changed: %q; error=%v", got.stdout, err)
						}
					case "text":
						if got.stdout != "ID: model_synthetic\nOwned by: synthetic\n" {
							t.Fatalf("text output changed: %q", got.stdout)
						}
					case "raw":
						if got.stdout != globalFlagsPage+"\n" {
							t.Fatalf("raw page changed: %q", got.stdout)
						}
					case "extraction":
						if got.stdout != "model_synthetic\n" {
							t.Fatalf("extraction changed: %q", got.stdout)
						}
					}
				})
			}
		}
	}
}

func TestMainGlobalFlagsPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  []string
		want []string
	}{
		{"unset", []string{"models", "list"}, nil, nil},
		{"environment", []string{"models", "list"}, []string{"OPENAI_PROJECT_ID=proj-env"}, []string{"proj-env"}},
		{"empty environment", []string{"models", "list"}, []string{"OPENAI_PROJECT_ID="}, []string{""}},
		{"empty spaced", []string{"models", "--project", "", "list"}, []string{"OPENAI_PROJECT_ID=proj-env"}, []string{""}},
		{"empty equals", []string{"models", "list", "--project="}, []string{"OPENAI_PROJECT_ID=proj-env"}, []string{""}},
		{"last duplicate", []string{"--project=first", "models", "--project", "second", "list", "--project=last"}, []string{"OPENAI_PROJECT_ID=proj-env"}, []string{"last"}},
		{"custom environment header", []string{"models", "list"}, []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: custom-env"}, []string{"custom-env"}},
		{"flag overrides environment header", []string{"models", "list", "--project=explicit"}, []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: custom-env"}, []string{"explicit"}},
		{"header overrides project", []string{"--header", "OpenAI-Project: header-first", "models", "--project=explicit", "list", "-H", "OpenAI-Project: header-last"}, nil, []string{"header-last"}},
		{"empty header", []string{"models", "list", "--project=explicit", "-H=OpenAI-Project:"}, nil, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := globalFlagsServer(t, globalFlagsPage)
			got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, tc.env...), append([]string{"openai", "--format=raw"}, tc.args...)...)
			if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsPage+"\n" {
				t.Fatalf("command failed: %+v", got)
			}
			if got := globalFlagsOneRequest(t, requests).header.Values("OpenAI-Project"); !slices.Equal(got, tc.want) {
				t.Fatalf("project header = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestMainGlobalFlagsHeaderAliasesAndAuthorization(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, "OPENAI_CUSTOM_HEADERS=Authorization: Bearer synthetic-env-override"),
		"openai", "--header", "X-Synthetic: first", "models", "-H", "X-Synthetic: second", "list",
		"--header=X-Synthetic: last", "-H=Authorization: Bearer synthetic-header-override", "--format=raw")
	if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsPage+"\n" {
		t.Fatalf("command failed: %+v", got)
	}
	request := globalFlagsOneRequest(t, requests)
	for name, want := range map[string]string{"X-Synthetic": "last", "Authorization": "Bearer synthetic-header-override"} {
		if got := request.header.Values(name); !slices.Equal(got, []string{want}) {
			t.Errorf("%s = %q; want %q", name, got, want)
		}
	}
}

func TestMainGlobalFlagsKeepEndpointFlagsAndLiterals(t *testing.T) {
	for _, tc := range []struct {
		name, project, path string
		args                []string
		failure             bool
	}{
		{"local flag before resource", "", "", []string{"--model", "synthetic", "models", "retrieve"}, true},
		{"local flag before operation", "", "", []string{"models", "--model", "synthetic", "retrieve"}, true},
		{"literal project value", "--organization", "/models", []string{"models", "list", "--project", "--organization"}, false},
		{"literal delimiter value", "--", "/models", []string{"models", "list", "--project", "--"}, false},
		{"completion-looking project", "__complete", "/models", []string{"models", "--project=__complete", "list"}, false},
		{"literal model flag value", "", "/models/--project", []string{"models", "retrieve", "--model", "--project"}, false},
		{"literal format-looking value", "", "/models/--format=json", []string{"models", "retrieve", "--model", "--format=json"}, false},
		{"literal after delimiter", "", "/models/--project", []string{"models", "retrieve", "--", "--project"}, false},
		{"delimiter stops global parsing", "", "", []string{"models", "list", "--", "--project=not-a-flag"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := globalFlagsServer(t, globalFlagsModel)
			got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), append([]string{"openai", "--format=raw"}, tc.args...)...)
			if tc.failure {
				if got.code != 1 || got.stderr == "" || len(requests) != 0 {
					t.Fatalf("expected local parse failure without requests: %+v; requests=%d", got, len(requests))
				}
				return
			}
			if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsModel+"\n" {
				t.Fatalf("literal command failed: %+v", got)
			}
			request := globalFlagsOneRequest(t, requests)
			if request.path != tc.path || request.header.Get("OpenAI-Project") != tc.project || request.header.Get("OpenAI-Organization") != "" {
				t.Fatalf("literal changed request: path=%q project=%q organization=%q", request.path, request.header.Get("OpenAI-Project"), request.header.Get("OpenAI-Organization"))
			}
		})
	}
}

func TestMainGlobalFlagsProjectBodyKeepsRootHeader(t *testing.T) {
	for _, route := range [][]string{{"admin", "organization", "invites"}, {"admin:organization:invites"}} {
		for _, project := range []struct {
			name            string
			args, env, want []string
		}{
			{"unset", nil, nil, nil},
			{"environment", nil, []string{"OPENAI_PROJECT_ID=proj-env"}, []string{"proj-env"}},
			{"root", []string{"--project=proj-root"}, nil, []string{"proj-root"}},
			{"empty", []string{"--project="}, []string{"OPENAI_PROJECT_ID=proj-env"}, []string{""}},
		} {
			t.Run(strings.Join(route, "/")+"/"+project.name, func(t *testing.T) {
				server, requests := globalFlagsServer(t, `{"id":"invite_synthetic"}`)
				args := append([]string{"openai", "--format=json"}, project.args...)
				args = append(args, route...)
				args = append(args, "create", "--email=synthetic@example.invalid", "--role=reader", "--project", `{"id":"membership_project","role":"member"}`)
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, append([]string{"OPENAI_ADMIN_KEY=synthetic-admin-key"}, project.env...)...), args...)
				if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) {
					t.Fatalf("invite command failed: %+v", got)
				}
				request := globalFlagsOneRequest(t, requests)
				if request.method != http.MethodPost || request.path != "/organization/invites" || !slices.Equal(request.header.Values("OpenAI-Project"), project.want) || request.header.Get("Authorization") != "Bearer synthetic-admin-key" {
					t.Fatalf("invite context changed: method=%s path=%q project=%q", request.method, request.path, request.header.Values("OpenAI-Project"))
				}
				var body map[string]any
				if err := json.Unmarshal(request.body, &body); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"email": "synthetic@example.invalid", "role": "reader", "projects": []any{map[string]any{"id": "membership_project", "role": "member"}}}
				if !reflect.DeepEqual(body, want) {
					t.Fatalf("invite body = %#v; want %#v", body, want)
				}
			})
		}
	}
}

func TestMainGlobalFlagsDeepAdminAndCustomCommands(t *testing.T) {
	command := []string{"admin", "organization", "invites", "list"}
	for position := 0; position <= len(command); position++ {
		t.Run(strings.Join(command[:position], "/"), func(t *testing.T) {
			server, requests := globalFlagsServer(t, `{"object":"list","data":[]}`)
			flags := []string{"--project=proj-deep", "--organization=org-deep", "--admin-api-key=synthetic-admin-key", "--format=raw"}
			got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), globalFlagsAt(command, flags, position)...)
			if got.code != 0 || got.stderr != "" || got.stdout != "{\"object\":\"list\",\"data\":[]}\n" {
				t.Fatalf("deep command failed: %+v", got)
			}
			request := globalFlagsOneRequest(t, requests)
			if request.path != "/organization/invites" || request.header.Get("OpenAI-Project") != "proj-deep" || request.header.Get("OpenAI-Organization") != "org-deep" || request.header.Get("Authorization") != "Bearer synthetic-admin-key" {
				t.Fatalf("deep command context changed: path=%q project=%q", request.path, request.header.Get("OpenAI-Project"))
			}
		})
	}
	for position := 0; position <= 2; position++ {
		t.Run("custom/"+[]string{"before", "between", "after"}[position], func(t *testing.T) {
			server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("OpenAI-Project") != "proj-custom" || r.Header.Get("Authorization") != "Bearer synthetic-explicit-key" {
					t.Error("custom command lost explicit request context")
				}
				mainImageModelResponse(w, r.URL.Path, "null")
			})
			got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), globalFlagsAt([]string{"images", "models"}, []string{"--project=proj-custom", "--api-key=synthetic-explicit-key", "--format=json"}, position)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("custom command failed: %+v", got)
			}
			report := decodeMainImageModels(t, got.stdout)
			if !report.Complete || report.Source != "live" || len(report.Models) == 0 || len(requests()) == 0 {
				t.Fatalf("custom command did not return verified models: %+v", report)
			}
		})
	}
}

func TestMainGlobalFlagsHelpAndCompletionStayLocal(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	for _, args := range [][]string{
		{"--project=proj-help", "models", "list", "--help"},
		{"models", "--project=proj-help", "list", "--help"},
		{"models", "list", "--project=proj-help", "--help"},
		{"help", "--all", "models", "list"},
		{"__complete", "--", "models", "list", "--project", ""},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			// The process helper removes inherited credentials. Local operations
			// must not initialize requests even when a usable endpoint exists.
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL}, append([]string{"openai"}, args...)...)
			if args[0] == "__complete" {
				if got.code != 0 && got.code != 11 || got.stderr != "" {
					t.Fatalf("completion failed: %+v", got)
				}
			} else if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "models") {
				t.Fatalf("help failed without credentials: %+v", got)
			}
			if len(requests) != 0 {
				t.Fatalf("local operation made %d requests", len(requests))
			}
		})
	}
}

func TestMainGlobalFlagsErrorOutputPlacement(t *testing.T) {
	for _, mode := range []string{"json", "text", "raw", "extraction"} {
		for position, placement := range []string{"before", "between", "after"} {
			t.Run(mode+"/"+placement, func(t *testing.T) {
				const detail = `{"message":"synthetic rejection","type":"invalid_request_error","code":"synthetic_code"}`
				requests := make(chan http.Header, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests <- r.Header.Clone()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					if _, err := io.WriteString(w, `{"error":`+detail+`}`); err != nil {
						t.Error(err)
					}
				}))
				t.Cleanup(server.Close)
				flags := []string{"--project=proj-error", "--format=json"}
				if mode == "extraction" {
					flags = append(flags, "--format-error=json", "--transform-error", "message")
				} else {
					flags = append(flags, "--format-error", mode)
				}
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), globalFlagsAt([]string{"models", "list"}, flags, position)...)
				if got.code != 1 || got.stdout != "" || len(requests) != 1 {
					t.Fatalf("API error lost exit status or output routing: %+v; requests=%d", got, len(requests))
				}
				if (<-requests).Get("OpenAI-Project") != "proj-error" {
					t.Fatal("error request lost project context")
				}
				switch mode {
				case "text":
					if !strings.Contains(got.stderr, "The API rejected the request") || strings.Contains(got.stderr, "synthetic rejection") {
						t.Fatalf("readable error changed: %q", got.stderr)
					}
				case "extraction":
					if got.stderr != "\"synthetic rejection\"\n" {
						t.Fatalf("error extraction changed: %q", got.stderr)
					}
				default:
					var actual, expected any
					if err := json.Unmarshal([]byte(got.stderr), &actual); err != nil {
						t.Fatalf("error is not one JSON document: %q; error=%v", got.stderr, err)
					}
					if err := json.Unmarshal([]byte(detail), &expected); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(actual, expected) || mode == "raw" && got.stderr != detail+"\n" {
						t.Fatalf("API error payload changed: %q", got.stderr)
					}
				}
			})
		}
	}
}
