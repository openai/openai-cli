package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type responsesFixtureTransport struct {
	endpoint  *url.URL
	transport http.Transport
}

func (f *responsesFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// Always connect to the loopback fixture while preserving the requested host
	// for assertions. No invocation can reach a real OpenAI or custom endpoint.
	redirected := request.Clone(request.Context())
	redirected.URL.Scheme, redirected.URL.Host = f.endpoint.Scheme, f.endpoint.Host
	redirected.Host = request.URL.Host
	return f.transport.RoundTrip(redirected)
}

func installResponsesFixture(t *testing.T, endpoint string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	require.Equal(t, "http", parsed.Scheme)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	http.DefaultTransport = &responsesFixtureTransport{endpoint: parsed}
}

func TestMainResponsesDefaultModel(t *testing.T) {
	const model = "gpt-6.2-sol"
	const base = "https://api.openai.com/v1"
	const prompt = "what day is it"
	writeInput := func(value string) string {
		path := filepath.Join(t.TempDir(), "input")
		require.NoError(t, os.WriteFile(path, []byte(value), 0600))
		return path
	}
	requestFile := writeInput(`{"input":"file prompt","model":"file-model"}`)
	defaultFile := writeInput("input: file prompt\n")
	textFile := writeInput("file text")
	for _, tc := range []struct {
		name, stdin, resource, extraBase string
		options                          []string
		want                             map[string]any
		stream, rejected                 bool
	}{
		{name: "default", options: []string{"--input", prompt}, want: map[string]any{"input": prompt, "model": model}},
		{name: "explicit model", options: []string{"--input", prompt, "--model", "explicit-model"}, want: map[string]any{"input": prompt, "model": "explicit-model"}},
		{name: "explicit empty model", options: []string{"--input", prompt, "--model="}, want: map[string]any{"input": prompt, "model": ""}},
		{name: "piped null model", stdin: `{"input":"fixture","model":null}`, want: map[string]any{"input": "fixture", "model": nil}},
		{name: "named request file default", options: []string{"--request-file", defaultFile}, want: map[string]any{"input": "file prompt", "model": model}},
		{name: "named request file model", options: []string{"--request-file", requestFile}, want: map[string]any{"input": "file prompt", "model": "file-model"}},
		{name: "flag overrides request file", options: []string{"--request-file", requestFile, "--model", "override"}, want: map[string]any{"input": "file prompt", "model": "override"}},
		{name: "request file stdin", stdin: `{"input":"stdin prompt"}`, options: []string{"--request-file", "-"}, want: map[string]any{"input": "stdin prompt", "model": model}},
		{name: "input file", options: []string{"--input", "@" + textFile}, want: map[string]any{"input": "file text", "model": model}},
		{name: "piped YAML", stdin: "input: pipe prompt\n", want: map[string]any{"input": "pipe prompt", "model": model}},
		{name: "structured input", stdin: `{"input":[{"role":"user","content":"fixture"}]}`, want: map[string]any{"input": []any{map[string]any{"role": "user", "content": "fixture"}}, "model": model}},
		{name: "saved prompt", options: []string{"--input", prompt, "--prompt.id", "pmpt_fixture"}, want: map[string]any{"input": prompt, "prompt": map[string]any{"id": "pmpt_fixture"}}},
		{name: "continuation", stdin: `{"input":"fixture","previous_response_id":"resp_fixture"}`, want: map[string]any{"input": "fixture", "previous_response_id": "resp_fixture"}},
		{name: "conversation", stdin: `{"input":"fixture","conversation":{"id":"conv_fixture"}}`, want: map[string]any{"input": "fixture", "conversation": map[string]any{"id": "conv_fixture"}}},
		{name: "empty input", stdin: `{"input":""}`, want: map[string]any{"input": ""}},
		{name: "whitespace input", options: []string{"--input", " \t "}, want: map[string]any{"input": " \t "}},
		{name: "null input", stdin: `{"input":null}`, want: map[string]any{"input": nil}},
		{name: "options only", stdin: `{"temperature":0}`, want: map[string]any{"temperature": float64(0)}},
		{name: "custom endpoint override", extraBase: "https://provider.invalid/v1", options: []string{"--input", prompt}, want: map[string]any{"input": prompt}},
		{name: "beta endpoint", resource: "beta:responses", options: []string{"--input", prompt}, want: map[string]any{"input": prompt}},
		{name: "regional endpoint", extraBase: "https://eu.api.openai.com/v1/", options: []string{"--input", prompt}, want: map[string]any{"input": prompt, "model": model}},
		{name: "US endpoint", extraBase: "https://us.api.openai.com/v1", options: []string{"--input", prompt}, want: map[string]any{"input": prompt, "model": model}},
		{name: "UAE endpoint", extraBase: "https://ae.api.openai.com/v1", options: []string{"--input", prompt}, want: map[string]any{"input": prompt}},
		{name: "UAE explicit model", extraBase: "https://ae.api.openai.com/v1", options: []string{"--input", prompt, "--model", "regional-model"}, want: map[string]any{"input": prompt, "model": "regional-model"}},
		{name: "streaming", options: []string{"--input", prompt, "--stream=true"}, stream: true, want: map[string]any{"input": prompt, "model": model, "stream": true}},
		{name: "default rejected once", options: []string{"--input", prompt}, rejected: true, want: map[string]any{"input": prompt, "model": model}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := base
			if tc.extraBase != "" {
				endpoint = tc.extraBase
			}
			target, err := url.Parse(endpoint)
			require.NoError(t, err)
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.URL.Path != "/v1/responses" || req.Host != target.Host {
					t.Errorf("unexpected fixture request: %s %s %s", req.Method, req.Host, req.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				select {
				case requests <- body:
				default:
					t.Error("too many requests")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.rejected {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"error":{"code":"model_not_found","message":"synthetic model rejection","param":"model","type":"invalid_request_error"}}`)
					return
				}
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"completed\"}}\n\n")
					return
				}
				_, _ = io.WriteString(w, `{"id":"resp_fixture","object":"response","status":"completed"}`)
			}))
			defer server.Close()
			env := adminCredentialsProxyEnv(server.URL, []string{
				"OPENAI_CLI_TEST_RESPONSES_SERVER=" + server.URL, "OPENAI_BASE_URL=" + base,
				"OPENAI_API_KEY=sk-fake-responses-default", "OPENAI_CLI_NO_AUTO_UPDATE=1", "NO_COLOR=1",
			})
			var stdin *os.File
			if tc.stdin != "" {
				stdin, err = os.Open(writeInput(tc.stdin))
				require.NoError(t, err)
				defer stdin.Close()
			}
			resource := tc.resource
			if resource == "" {
				resource = "responses"
			}
			args := []string{"openai", "--format", "json", resource, "create"}
			if tc.extraBase != "" {
				args = append(args, "--base-url", tc.extraBase)
			}
			got := runMainDispatchWithStdin(t, "bash", env, stdin, append(args, tc.options...)...)
			require.Len(t, requests, 1, "%+v", got)
			require.Equal(t, tc.want, <-requests)
			if tc.rejected {
				require.Equal(t, 1, got.code)
				require.Empty(t, got.stdout)
				require.Equal(t, "model_not_found", decodeMainErrorObject(t, "json", got.stderr)["code"])
				return
			}
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "resp_fixture")
		})
	}
}

func TestMainResponsesDefaultModelHelp(t *testing.T) {
	for _, args := range [][]string{
		{"responses", "create", "--help"}, {"help", "--all", "responses", "create"}, {"responses", "create", "-h", "--model"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "gpt-6.2-sol")
			require.Contains(t, strings.Join(strings.Fields(got.stdout), " "), "on global, US, and EU OpenAI endpoints.")
			if len(args) == 3 {
				require.Contains(t, got.stdout, `openai responses create --input "Say hello"`)
			}
		})
	}
	got := runMainDispatch(t, "bash", "openai", "beta:responses", "create", "--help")
	require.Zero(t, got.code)
	require.NotContains(t, got.stdout, "gpt-6.2-sol")
}
