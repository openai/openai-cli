package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMainGlobalFlagBaseURLOverridesMalformedEnvironment(t *testing.T) {
	for _, malformed := range []struct{ name, value string }{
		{"missing scheme", "not-a-request-url"},
		{"invalid relative escape", "not%url"},
		{"invalid absolute escape", "http://127.0.0.1/%zz"},
	} {
		for _, equals := range []bool{false, true} {
			for position, placement := range []string{"before", "between", "after"} {
				t.Run(malformed.name+"/"+map[bool]string{false: "spaced", true: "equals"}[equals]+"/"+placement, func(t *testing.T) {
					server, requests := globalFlagsServer(t, globalFlagsPage)
					flags := []string{"--base-url", server.URL}
					if equals {
						flags = []string{"--base-url=" + server.URL}
					}
					flags = append(flags, "--project=proj-explicit", "--format=raw")
					got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, "OPENAI_BASE_URL="+malformed.value),
						globalFlagsAt([]string{"models", "list"}, flags, position)...)
					if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsPage+"\n" {
						t.Fatalf("explicit endpoint did not override the environment: %+v", got)
					}
					request := globalFlagsOneRequest(t, requests)
					if request.method != http.MethodGet || request.path != "/models" || request.header.Get("OpenAI-Project") != "proj-explicit" {
						t.Fatalf("unexpected request: method=%q path=%q project=%q", request.method, request.path, request.header.Get("OpenAI-Project"))
					}
					if request.header.Get("Authorization") != "Bearer synthetic-env-key" {
						t.Fatal("endpoint override changed the environment credential")
					}
				})
			}
		}
	}
}

func TestMainGlobalFlagInvalidPercentBaseURLStillFails(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	for _, malformed := range []struct{ name, value string }{
		{"relative", "not%url"},
		{"absolute", server.URL + "/%zz"},
	} {
		for _, config := range []struct {
			name, env string
			flags     []string
		}{
			{"environment", malformed.value, nil},
			{"empty spaced fallback", malformed.value, []string{"--base-url", ""}},
			{"empty equals fallback", malformed.value, []string{"--base-url="}},
			{"explicit", server.URL, []string{"--base-url", malformed.value}},
		} {
			t.Run(malformed.name+"/"+config.name, func(t *testing.T) {
				args := append([]string{"openai", "models", "list", "--format=raw"}, config.flags...)
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, "OPENAI_BASE_URL="+config.env), args...)
				if got.code != 1 || got.stdout != "" || got.stderr == "" || len(requests) != 0 {
					t.Fatalf("selected invalid endpoint must fail without requests: %+v; requests=%d", got, len(requests))
				}
			})
		}
	}
}

func TestMainGlobalFlagBaseURLOverridePreservesEnvironmentHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command []string
		flags   []string
		want    map[string]string
	}{
		{"API defaults", []string{"models", "list"}, nil, nil},
		{"explicit context", []string{"models", "list"}, []string{"--organization=org-explicit", "--project=proj-explicit"},
			map[string]string{"OpenAI-Organization": "org-explicit", "OpenAI-Project": "proj-explicit"}},
		{"header precedence", []string{"models", "list"}, []string{
			"--project=proj-explicit", "--header=OpenAI-Project: proj-first", "-H=OpenAI-Project: proj-last",
			"--header=X-Synthetic-Custom: first", "-H=X-Synthetic-Custom: last",
		}, map[string]string{"OpenAI-Project": "proj-last", "X-Synthetic-Custom": "last"}},
		{"admin defaults", []string{"admin", "organization", "invites", "list"}, nil,
			map[string]string{"Authorization": "Bearer synthetic-admin-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const response = `{"object":"list","data":[]}`
			server, requests := globalFlagsServer(t, response)
			env := globalFlagsEnv(server, "OPENAI_BASE_URL=not%url", "OPENAI_ADMIN_KEY=synthetic-admin-key",
				"OPENAI_ORG_ID=org-env", "OPENAI_PROJECT_ID=proj-env",
				"OPENAI_CUSTOM_HEADERS=X-Synthetic-Custom: env-first\nx-synthetic-custom: env-last\nX-Synthetic-Only: env-only")
			args := append([]string{"openai"}, tc.command...)
			args = append(args, "--base-url="+server.URL, "--format=raw")
			args = append(args, tc.flags...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || got.stdout != response+"\n" {
				t.Fatalf("endpoint override lost environment request options: %+v", got)
			}
			request := globalFlagsOneRequest(t, requests)
			wantPath := "/models"
			if tc.command[0] == "admin" {
				wantPath = "/organization/invites"
			}
			if request.method != http.MethodGet || request.path != wantPath {
				t.Fatalf("unexpected request: %s %s", request.method, request.path)
			}
			want := map[string]string{
				"Authorization": "Bearer synthetic-env-key", "OpenAI-Organization": "org-env", "OpenAI-Project": "proj-env",
				"X-Synthetic-Custom": "env-last", "X-Synthetic-Only": "env-only",
			}
			for name, value := range tc.want {
				want[name] = value
			}
			for name, value := range want {
				if !slices.Equal(request.header.Values(name), []string{value}) {
					t.Errorf("endpoint override changed %s values or precedence", name)
				}
			}
		})
	}
}

func TestMainGlobalFlagInvalidPercentBaseURLErrorsAreSafe(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	malformed := "http://synthetic-private-user:synthetic-private-password@" + strings.TrimPrefix(server.URL, "http://") +
		"/synthetic-private-path/%zz?token=synthetic-private-query"
	for _, format := range []string{"text", "json"} {
		for _, config := range []struct {
			name, env string
			flags     []string
		}{
			{"environment", malformed, nil},
			{"empty fallback", malformed, []string{"--base-url="}},
			{"explicit", server.URL, []string{"--base-url", malformed}},
		} {
			t.Run(format+"/"+config.name, func(t *testing.T) {
				args := append([]string{"openai", "models", "list", "--format-error=" + format}, config.flags...)
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, "OPENAI_BASE_URL="+config.env,
					"OPENAI_API_KEY=synthetic-private-key", "OPENAI_CUSTOM_HEADERS=X-Synthetic-Secret: synthetic-private-header"), args...)
				if got.code != 1 || got.stdout != "" || got.stderr == "" || len(requests) != 0 {
					t.Fatalf("invalid endpoint must fail without requests: %+v; requests=%d", got, len(requests))
				}
				if strings.Contains(got.stderr, "synthetic-private-") || strings.Contains(got.stderr, "token=") {
					t.Fatal("invalid endpoint diagnostic exposed synthetic sensitive values")
				}
				if format == "json" {
					decodeMainStructuredError(t, format, got.stderr)
				}
			})
		}
	}
}

func TestMainGlobalFlagEmptyBaseURLKeepsEnvironmentFallback(t *testing.T) {
	for _, equals := range []bool{false, true} {
		for position, placement := range []string{"before", "between", "after"} {
			t.Run(map[bool]string{false: "spaced", true: "equals"}[equals]+"/"+placement, func(t *testing.T) {
				server, requests := globalFlagsServer(t, globalFlagsPage)
				flags := []string{"--base-url", ""}
				if equals {
					flags = []string{"--base-url="}
				}
				flags = append(flags, "--format=raw")
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), globalFlagsAt([]string{"models", "list"}, flags, position)...)
				if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsPage+"\n" {
					t.Fatalf("empty endpoint did not retain the environment fallback: %+v", got)
				}
				if request := globalFlagsOneRequest(t, requests); request.method != http.MethodGet || request.path != "/models" {
					t.Fatalf("unexpected request: %s %s", request.method, request.path)
				}
			})
		}
	}
}

func TestMainGlobalFlagEmptyMTLSOverridesEnvironment(t *testing.T) {
	for _, equals := range []bool{false, true} {
		for position, placement := range []string{"before", "between", "after"} {
			t.Run(map[bool]string{false: "spaced", true: "equals"}[equals]+"/"+placement, func(t *testing.T) {
				server, requests := globalFlagsServer(t, globalFlagsPage)
				flags := []string{"--mtls-client-cert-file", "", "--mtls-client-key-file", ""}
				if equals {
					flags = []string{"--mtls-client-cert-file=", "--mtls-client-key-file="}
				}
				flags = append(flags, "--format=raw")
				missing := filepath.Join(t.TempDir(), "missing.pem")
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server,
					"OPENAI_MTLS_CLIENT_CERT_FILE="+missing, "OPENAI_MTLS_CLIENT_KEY_FILE="+missing),
					globalFlagsAt([]string{"models", "list"}, flags, position)...)
				if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsPage+"\n" {
					t.Fatalf("explicit empty mTLS flags did not override environment defaults: %+v", got)
				}
				if request := globalFlagsOneRequest(t, requests); request.method != http.MethodGet || request.path != "/models" {
					t.Fatalf("unexpected request: %s %s", request.method, request.path)
				}
			})
		}
	}
}

func TestMainGlobalFlagMTLSRejectsExplicitDuplicates(t *testing.T) {
	for _, name := range []string{"mtls-client-cert-file", "mtls-client-key-file"} {
		for _, args := range [][]string{
			{"openai", "--" + name + "=", "--" + name + "=", "models", "list"},
			{"openai", "--" + name + "=", "models", "--" + name + "=", "list"},
			{"openai", "models", "--" + name + "=", "list", "--" + name + "="},
		} {
			t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
				server, requests := globalFlagsServer(t, globalFlagsPage)
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), args...)
				if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, name) || len(requests) != 0 {
					t.Fatalf("duplicate mTLS flag must fail without requests: %+v; requests=%d", got, len(requests))
				}
			})
		}
	}
}

func TestMainGlobalFlagCompletionScriptsIgnoreRequestConfiguration(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	missing := filepath.Join(t.TempDir(), "missing.pem")
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		want := runMainDispatchWithEnv(t, style, []string{"OPENAI_BASE_URL=" + server.URL}, "openai", "@completion", style)
		if want.code != 0 || want.stderr != "" || !strings.Contains(want.stdout, "__complete") {
			t.Fatalf("completion script control failed: %+v", want)
		}
		for _, config := range []struct {
			name string
			env  []string
		}{
			{"endpoint", nil},
			{"invalid endpoint", []string{"OPENAI_BASE_URL=not-a-request-url"}},
			{"certificate only", []string{"OPENAI_MTLS_CLIENT_CERT_FILE=" + missing}},
			{"key only", []string{"OPENAI_MTLS_CLIENT_KEY_FILE=" + missing}},
			{"missing files", []string{"OPENAI_BASE_URL=" + strings.Replace(server.URL, "http://", "https://", 1), "OPENAI_MTLS_CLIENT_CERT_FILE=" + missing, "OPENAI_MTLS_CLIENT_KEY_FILE=" + missing}},
		} {
			t.Run(style+"/"+config.name, func(t *testing.T) {
				// The subprocess harness removes inherited OpenAI credentials.
				env := append([]string{"OPENAI_BASE_URL=" + server.URL}, config.env...)
				got := runMainDispatchWithEnv(t, style, env, "openai", "@completion", style)
				if got != want {
					t.Fatalf("local completion script depends on request configuration: %+v", got)
				}
				if len(requests) != 0 {
					t.Fatalf("completion script made %d requests", len(requests))
				}
			})
		}
	}
}

func TestMainGlobalFlagCompletionWordsRemainRequestValues(t *testing.T) {
	for _, value := range []string{"@completion", "__complete"} {
		for _, equals := range []bool{false, true} {
			t.Run(value+"/"+map[bool]string{false: "spaced", true: "equals"}[equals], func(t *testing.T) {
				server, requests := globalFlagsServer(t, globalFlagsModel)
				args := []string{"openai", "models", "retrieve", "--format=raw"}
				if equals {
					args = append(args, "--model="+value)
				} else {
					args = append(args, "--model", value)
				}
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), args...)
				if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsModel+"\n" {
					t.Fatalf("completion-looking request value changed command dispatch: %+v", got)
				}
				if request := globalFlagsOneRequest(t, requests); request.method != http.MethodGet || request.path != "/models/"+value {
					t.Fatalf("unexpected request: %s %s", request.method, request.path)
				}
			})
		}
	}
}

func TestMainGlobalFlagRequestConfigurationStillValidates(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	missing := filepath.Join(t.TempDir(), "missing.pem")
	for _, config := range []struct {
		name, want string
		env, flags []string
	}{
		{"invalid explicit endpoint", "--base-url", nil, []string{"--base-url=not-a-request-url"}},
		{"invalid environment", "OPENAI_BASE_URL", []string{"OPENAI_BASE_URL=not-a-request-url"}, nil},
		{"empty endpoint fallback", "OPENAI_BASE_URL", []string{"OPENAI_BASE_URL=not-a-request-url"}, []string{"--base-url="}},
		{"certificate only", "mTLS", []string{"OPENAI_MTLS_CLIENT_CERT_FILE=" + missing}, nil},
		{"key only", "mTLS", []string{"OPENAI_MTLS_CLIENT_KEY_FILE=" + missing}, nil},
		{"missing files", "mTLS", []string{"OPENAI_BASE_URL=" + strings.Replace(server.URL, "http://", "https://", 1), "OPENAI_MTLS_CLIENT_CERT_FILE=" + missing, "OPENAI_MTLS_CLIENT_KEY_FILE=" + missing}, nil},
	} {
		for _, value := range []string{"ordinary", "@completion", "__complete"} {
			t.Run(config.name+"/"+value, func(t *testing.T) {
				args := append([]string{"openai", "models", "retrieve", "--model", value}, config.flags...)
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, config.env...), args...)
				if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, config.want) || len(requests) != 0 {
					t.Fatalf("request configuration validation changed: %+v; requests=%d", got, len(requests))
				}
			})
		}
	}
}

func TestMainGlobalFlagURLConfigurationPreservesOfflineHelp(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	for _, test := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"help"}, true},
		{[]string{"help", "setup"}, true},
		{[]string{"help", "models", "list"}, true},
		{[]string{"help", "imaginary"}, false},
		{[]string{"help", "models", "imaginary"}, false},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(strings.Join(test.args, "/")+"/"+format, func(t *testing.T) {
				args := append([]string{"openai", "--format-error", format}, test.args...)
				want := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server), args...)
				if test.ok && (want.code != 0 || want.stdout == "" || want.stderr != "") {
					t.Fatalf("valid help control failed: %+v", want)
				}
				if !test.ok && (want.code == 0 || want.stdout != "" || want.stderr == "") {
					t.Fatalf("unknown help topic control lost its error: %+v", want)
				}
				env := globalFlagsEnv(server, "OPENAI_BASE_URL=not%url", "OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic/missing.crt", "OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic/missing.key")
				if got := runMainDispatchWithEnv(t, "bash", env, args...); got != want {
					t.Fatalf("request configuration changed local help: got %+v; want %+v", got, want)
				}
				if len(requests) != 0 {
					t.Fatalf("local help made %d requests", len(requests))
				}
			})
		}
	}
}
