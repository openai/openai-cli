package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainBaseURLRejectsRemoteHTTP(t *testing.T) {
	for _, command := range [][]string{
		{"models", "list"}, {"admin", "organization", "projects", "list"},
	} {
		for _, endpoint := range []string{
			"http://remote.example/v1", "http://192.0.2.1/v1", "http://[2001:db8::1]/v1",
			"http://localhost:fake-password@remote.example/v1?token=fake-private",
		} {
			for _, source := range []string{"flag", "environment"} {
				t.Run(strings.Join(command, "/")+"/"+source+"/"+endpoint, func(t *testing.T) {
					var requests atomic.Int32
					// If validation regresses, the local proxy receives only synthetic
					// credentials and returns a response without contacting a backend.
					proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
					}))
					defer proxy.Close()
					env := adminCredentialsProxyEnv(proxy.URL, []string{
						"OPENAI_API_KEY=synthetic-api-key", "OPENAI_ADMIN_KEY=synthetic-admin-key",
					})
					args := []string{"openai"}
					wantSource := "OPENAI_BASE_URL"
					if source == "flag" {
						args = append(args, "--base-url", endpoint)
						wantSource = "--base-url"
					} else {
						env = append(env, "OPENAI_BASE_URL="+endpoint)
					}
					got := runMainDispatchWithEnv(t, "bash", env, append(args, command...)...)
					if got.code != 1 || !strings.Contains(got.stderr, wantSource) || !strings.Contains(got.stderr, "HTTPS") {
						t.Errorf("expected actionable HTTPS rejection, got %+v", got)
					}
					if strings.Contains(got.stderr, "fake-") || strings.Contains(got.stderr, "synthetic-") {
						t.Error("rejection exposed URL or credential contents")
					}
					if requests.Load() != 0 {
						t.Errorf("sent %d requests before rejecting remote HTTP", requests.Load())
					}
				})
			}
		}
	}
}

func TestMainBaseURLPreservesLoopbackAndPrecedence(t *testing.T) {
	for _, command := range [][]string{
		{"models", "list"}, {"admin", "organization", "projects", "list"},
	} {
		for _, source := range []string{"flag", "environment", "flag overrides remote environment"} {
			t.Run(strings.Join(command, "/")+"/"+source, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					want := "Bearer synthetic-api-key"
					if command[0] == "admin" {
						want = "Bearer synthetic-admin-key"
					}
					if r.Header.Get("Authorization") != want {
						t.Error("request did not retain the applicable synthetic credential")
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
				}))
				defer server.Close()
				env := []string{"OPENAI_API_KEY=synthetic-api-key", "OPENAI_ADMIN_KEY=synthetic-admin-key"}
				args := []string{"openai", "--format", "json"}
				if source == "environment" {
					env = append(env, "OPENAI_BASE_URL="+server.URL)
				} else {
					args = append(args, "--base-url", server.URL)
					if source == "flag overrides remote environment" {
						env = append(env, "OPENAI_BASE_URL=http://remote.example/v1")
					}
				}
				got := runMainDispatchWithEnv(t, "bash", env, append(args, command...)...)
				if got.code != 0 || requests.Load() != 1 {
					t.Errorf("loopback request = %+v, requests = %d; want success and one request", got, requests.Load())
				}
			})
		}
	}
}
