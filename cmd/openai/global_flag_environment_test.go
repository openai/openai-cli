package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainGlobalFlagBaseURLOverridesMalformedEnvironment(t *testing.T) {
	for _, equals := range []bool{false, true} {
		for position, placement := range []string{"before", "between", "after"} {
			t.Run(map[bool]string{false: "spaced", true: "equals"}[equals]+"/"+placement, func(t *testing.T) {
				server, requests := globalFlagsServer(t, globalFlagsPage)
				flags := []string{"--base-url", server.URL}
				if equals {
					flags = []string{"--base-url=" + server.URL}
				}
				flags = append(flags, "--project=proj-explicit", "--format=raw")
				got := runMainDispatchWithEnv(t, "bash", globalFlagsEnv(server, "OPENAI_BASE_URL=not-a-request-url"),
					globalFlagsAt([]string{"models", "list"}, flags, position)...)
				if got.code != 0 || got.stderr != "" || got.stdout != globalFlagsPage+"\n" {
					t.Fatalf("explicit endpoint did not override the environment: %+v", got)
				}
				request := globalFlagsOneRequest(t, requests)
				if request.method != http.MethodGet || request.path != "/models" || request.header.Get("OpenAI-Project") != "proj-explicit" {
					t.Fatalf("unexpected request: method=%q path=%q project=%q", request.method, request.path, request.header.Get("OpenAI-Project"))
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
