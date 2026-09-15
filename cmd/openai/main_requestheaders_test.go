package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainRequestHeaders(t *testing.T) {
	requests := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"test-model","object":"model","created":0,"owned_by":"test"}`)
	}))
	t.Cleanup(server.Close)

	got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test",
		"--header", "X-Trace: first", "models", "-H", "X-Trace: middle", "retrieve", "--model", "test-model",
		"-H", "x-trace: \tsecond, third:fourth\t ", "--header", "X-Literal: @/not-a-file.txt", "-H", "X-Empty:")
	if got.code != 0 {
		t.Fatalf("main with request headers = %+v, want exit code 0", got)
	}
	select {
	case headers := <-requests:
		for name, want := range map[string]string{
			"X-Trace": "second, third:fourth", "X-Literal": "@/not-a-file.txt", "X-Empty": "",
		} {
			if values := headers.Values(name); len(values) != 1 || values[0] != want {
				t.Errorf("request header %s = %q, want [%q]", name, values, want)
			}
		}
	default:
		t.Error("main with request headers sent no request, want one")
	}
}

func TestMainRequestHeadersJSONAndStreaming(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, response string
		stream                      bool
	}{
		{
			name: "JSON", contentType: "application/json",
			response: `{"id":"resp_test","object":"response","status":"completed","output":[]}`,
		},
		{
			name: "streaming", contentType: "text/event-stream", stream: true,
			response: "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":0,\"response\":{\"id\":\"resp_test\",\"object\":\"response\",\"status\":\"completed\",\"output\":[]}}\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/responses" {
					t.Errorf("request = %s %s, want POST /responses", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("X-Trace"); got != "request-test" {
					t.Errorf("X-Trace = %q, want request-test", got)
				}
				if got := r.Header.Get("Content-Type"); got != "application/example+json" {
					t.Errorf("Content-Type = %q, want explicit application/example+json", got)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request body: %v", err)
				}
				if body["model"] != "test-model" || body["input"] != "test input" {
					t.Errorf("request body = %#v, want model test-model and input test input", body)
				}
				if stream, _ := body["stream"].(bool); stream != tc.stream {
					t.Errorf("request stream = %v, want %v", stream, tc.stream)
				}
				w.Header().Set("Content-Type", tc.contentType)
				io.WriteString(w, tc.response)
			}))
			t.Cleanup(server.Close)
			args := []string{"openai", "--base-url", server.URL, "--api-key", "sk-test", "--format", "jsonl",
				"responses", "create", "--model", "test-model", "--input", "test input",
				"-H", "X-Trace: request-test", "--header", "Content-Type: application/example+json"}
			if tc.stream {
				args = append(args, "--stream", "true")
			}
			got := runMainDispatch(t, "bash", args...)
			if got.code != 0 || !strings.Contains(got.stdout, "resp_test") {
				t.Errorf("main %s = %+v, want exit 0 and resp_test output", tc.name, got)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("request count = %d, want 1", got)
			}
		})
	}
}

func TestMainRequestHeadersMultipart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "example.txt")
	if err := os.WriteFile(file, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("X-Trace"); got != "upload-test" {
			t.Errorf("X-Trace = %q, want upload-test", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		part, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("read file part: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer part.Close()
		content, err := io.ReadAll(part)
		if err != nil || string(content) != "synthetic upload" || r.FormValue("purpose") != "user_data" {
			t.Errorf("upload = %q, purpose = %q, error = %v; want synthetic upload and user_data", content, r.FormValue("purpose"), err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"file_test","object":"file","bytes":16,"created_at":0,"filename":"example.txt","purpose":"user_data"}`)
	}))
	t.Cleanup(server.Close)
	got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test",
		"files", "create", "--file", file, "--purpose", "user_data", "-H", "X-Trace: upload-test")
	if got.code != 0 || requests.Load() != 1 {
		t.Errorf("main multipart = %+v, requests = %d; want exit 0 and one request", got, requests.Load())
	}
}

func TestMainRequestHeadersAuthenticationAndDebug(t *testing.T) {
	for _, tc := range []struct {
		name     string
		command  []string
		wantAuth string
	}{
		{name: "API", command: []string{"models", "retrieve", "--model", "test-model"}, wantAuth: "Bearer sk-api-test"},
		{name: "admin", command: []string{"admin:organization:users", "retrieve", "--user-id", "user_test"}, wantAuth: "Bearer sk-admin-test"},
	} {
		for _, override := range []bool{false, true} {
			name := tc.name + "/default"
			if override {
				name = tc.name + "/override"
			}
			t.Run(name, func(t *testing.T) {
				wantAuth, wantOrg := tc.wantAuth, "org-default"
				if override {
					wantAuth, wantOrg = "fake-credential-prefix fake-credential", "org-override"
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if got := r.Header.Get("Authorization"); got != wantAuth {
						t.Errorf("Authorization = %q, want %q", got, wantAuth)
					}
					if got := r.Header.Get("OpenAI-Organization"); got != wantOrg {
						t.Errorf("OpenAI-Organization = %q, want %q", got, wantOrg)
					}
					if override {
						if got := r.Header.Get("X-Custom-Token"); got != "fake-custom-token" {
							t.Errorf("X-Custom-Token = %q, want fake-custom-token", got)
						}
						w.Header().Set("X-Custom-Token", "fake-response-token")
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"test"}`)
				}))
				t.Cleanup(server.Close)
				args := []string{"openai", "--base-url", server.URL, "--api-key", "sk-api-test",
					"--admin-api-key", "sk-admin-test", "--organization", "org-default", "--debug"}
				args = append(args, tc.command...)
				if override {
					args = append(args, "-H", "Authorization: "+wantAuth,
						"-H", "OpenAI-Organization: org-override", "-H", "X-Custom-Token: fake-custom-token")
				}
				got := runMainDispatch(t, "bash", args...)
				if got.code != 0 || requests.Load() != 1 {
					t.Errorf("main auth = %+v, requests = %d; want exit 0 and one request", got, requests.Load())
				}
				if !strings.Contains(got.stderr, "Request Content:") || !strings.Contains(got.stderr, "<REDACTED>") {
					t.Errorf("debug output = %q, want redacted request details", got.stderr)
				}
				for _, secret := range []string{"sk-api-test", "sk-admin-test", "fake-credential", "fake-custom-token", "fake-response-token"} {
					if strings.Contains(got.stdout+got.stderr, secret) {
						t.Errorf("main exposed synthetic credential %q in diagnostics", secret)
					}
				}
			})
		}
	}
}

func TestMainRequestHeadersFromEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name                string
		command             []string
		wantToken, wantAuth string
	}{
		{
			name: "API", command: []string{"models", "retrieve", "--model", "test-model"},
			wantToken: "fake-env-token", wantAuth: "Bearer sk-api-env-test",
		},
		{
			name: "admin", command: []string{"admin:organization:users", "retrieve", "--user-id", "user_test"},
			wantToken: "fake-env-token", wantAuth: "Bearer sk-admin-env-test",
		},
		{
			name: "flag override", command: []string{"models", "retrieve", "--model", "test-model", "-H", "x-custom-token: fake-flag-token"},
			wantToken: "fake-flag-token", wantAuth: "Bearer sk-api-env-test",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				for name, want := range map[string]string{
					"X-Custom-Token": tc.wantToken, "Authorization": tc.wantAuth, "X-Trace": "literal,env:value",
				} {
					if got := r.Header.Get(name); got != want {
						t.Errorf("environment request header %s = %q, want %q", name, got, want)
					}
				}
				w.Header().Set("X-Custom-Token", "fake-env-response-token")
				w.Header().Set("X-Trace", "fake-env-response-trace")
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"test"}`)
			}))
			t.Cleanup(server.Close)
			env := []string{
				"OPENAI_API_KEY=sk-api-env-test", "OPENAI_ADMIN_KEY=sk-admin-env-test",
				"OPENAI_CUSTOM_HEADERS= \tX-Custom-Token \t: fake-env-token\nX-Trace: literal,env:value",
			}
			args := append([]string{"openai", "--base-url", server.URL, "--debug"}, tc.command...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || requests.Load() != 1 {
				t.Errorf("main environment headers = %+v, requests = %d; want exit 0 and one request", got, requests.Load())
			}
			if !strings.Contains(got.stderr, "Request Content:") || !strings.Contains(got.stderr, "Response Content:") || !strings.Contains(got.stderr, "<REDACTED>") {
				t.Errorf("debug output = %q, want redacted request and response details", got.stderr)
			}
			for _, secret := range []string{"sk-api-env-test", "sk-admin-env-test", "fake-env-token", "fake-flag-token", "literal,env:value", "fake-env-response-token", "fake-env-response-trace"} {
				if strings.Contains(got.stdout+got.stderr, secret) {
					t.Errorf("main exposed synthetic environment header %q in diagnostics", secret)
				}
			}
		})
	}
}

func TestMainRequestHeadersOverrideEndpointHeader(t *testing.T) {
	requests := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_test","object":"response","output":[]}`)
	}))
	t.Cleanup(server.Close)
	got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test",
		"beta:responses", "retrieve", "--response-id", "resp_test", "--beta", "endpoint-value",
		"--header", "OpenAI-Beta: explicit-value")
	if got.code != 0 {
		t.Fatalf("main endpoint header = %+v, want exit 0", got)
	}
	select {
	case headers := <-requests:
		if got := headers.Values("OpenAI-Beta"); len(got) != 1 || got[0] != "explicit-value" {
			t.Errorf("OpenAI-Beta = %q, want [explicit-value]", got)
		}
	default:
		t.Error("main endpoint header sent no request, want one")
	}
}

func TestMainRequestHeadersRejectInvalidInput(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)
	for _, tc := range []struct{ name, value string }{
		{"missing colon", "fake-secret"},
		{"empty name", ": fake-secret"},
		{"space in name", "Bad Name: fake-secret"},
		{"non-ASCII name", "X-é: fake-secret"},
		{"newline", "X-Test: fake-secret\r\nX-Injected: yes"},
		{"control byte", "X-Test: fake-secret\x01"},
		{"DEL", "X-Test: fake-secret\x7f"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test",
				"files", "create", "--file", filepath.Join(t.TempDir(), "missing.txt"), "--purpose", "user_data",
				"--header", tc.value)
			if got.code == 0 || !strings.Contains(got.stderr, "header 1:") {
				t.Errorf("main malformed header = %+v, want header error before opening the missing file", got)
			}
			if strings.Contains(got.stdout+got.stderr, "fake-secret") {
				t.Errorf("main malformed header exposed its value: %+v", got)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("malformed headers sent %d requests, want 0", got)
	}
}

func TestMainRequestHeadersHelpAndCompletion(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "--header", "X-Test: fake-help-secret", "--help"},
		{"openai", "models", "retrieve", "-H", "X-Test: fake-help-secret", "--help"},
	} {
		got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_CUSTOM_HEADERS=X-Test: fake-env-help-secret"}, args...)
		if got.code != 0 || strings.Contains(got.stdout+got.stderr, "fake-help-secret") || strings.Contains(got.stdout+got.stderr, "fake-env-help-secret") {
			t.Errorf("main help with header = %+v, want exit 0 without header value", got)
		}
		if !strings.Contains(got.stdout, "OPENAI_CUSTOM_HEADERS") {
			t.Errorf("header help = %q, want the environment input documented", got.stdout)
		}
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(style, func(t *testing.T) {
			for _, scope := range [][]string{nil, {"models", "retrieve"}} {
				args := append([]string{"openai", "__complete"}, scope...)
				got := runMainDispatch(t, style, append(args, "--hea")...)
				if got.code != 0 || !strings.Contains(got.stdout, "--header") {
					t.Errorf("header flag completion = %+v, want --header suggestion", got)
				}
				for _, flag := range []string{"--header", "-H"} {
					got := runMainDispatch(t, style, append(args, flag, "@fake-completion-secret")...)
					if got.code != 11 || got.stdout != "" || got.stderr != "" {
						t.Errorf("header value completion = %+v, want no completions and exit 11", got)
					}
				}
			}
		})
	}
}

func TestMainRequestHeadersRejectCrossOriginRedirect(t *testing.T) {
	var requests, redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("X-Custom-Token"); got != "fake-redirect-secret" {
			t.Errorf("source request header = %q, want fake-redirect-secret", got)
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(server.Close)
	got := runMainDispatch(t, "bash", "openai", "--base-url", server.URL, "--api-key", "sk-test",
		"models", "retrieve", "--model", "test-model", "-H", "X-Custom-Token: fake-redirect-secret")
	if got.code == 0 || requests.Load() != 1 || redirected.Load() != 0 {
		t.Errorf("main redirect = %+v, source requests = %d, target requests = %d; want error, one source request, and zero target requests", got, requests.Load(), redirected.Load())
	}
	if strings.Contains(got.stdout+got.stderr, "fake-redirect-secret") {
		t.Errorf("redirect error exposed the header value: %+v", got)
	}
}
