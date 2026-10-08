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

	"github.com/stretchr/testify/require"
)

func localUtilitiesEnvironment(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	t.Cleanup(func() {
		entries, err := os.ReadDir(home)
		require.NoError(t, err)
		require.Empty(t, entries, "non-interactive utilities must not persist files")
	})
	return []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "CODEX_HOME=" + home,
		"XDG_CACHE_HOME=" + home, "TIKTOKEN_CACHE_DIR=" + home,
		"PATH=" + t.TempDir(), "OPENAI_PICKER_SHELL=off",
		"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost",
	}
}

func localUtilitiesRequestTrap(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"synthetic request trap","type":"authentication_error"}}`)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestMainLocalUtilitiesIgnoreRemoteConfiguration(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	for _, configuration := range []struct {
		name string
		env  []string
	}{
		{"loopback without credentials", []string{"OPENAI_BASE_URL=" + server.URL}},
		{"malformed remote environment", []string{
			"OPENAI_BASE_URL=://synthetic-private-endpoint",
			"OPENAI_CUSTOM_HEADERS=synthetic-private-headers",
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert",
			"HTTPS_PROXY=://synthetic-private-proxy",
		}},
	} {
		t.Run(configuration.name, func(t *testing.T) {
			env := append(localUtilitiesEnvironment(t), configuration.env...)
			for _, format := range []string{"text", "json"} {
				t.Run(format, func(t *testing.T) {
					count := runMainDispatchWithEnv(t, "bash", env,
						"openai", "tokenizer", "count", "--text", "Hello, world!", "--format", format)
					require.Zero(t, count.code, "%s", count.stderr)
					require.Empty(t, count.stderr)
					require.Contains(t, count.stdout, "o200k_base")
					if format == "json" {
						var result struct {
							Encoding   string `json:"encoding"`
							InputBytes int    `json:"input_bytes"`
							TokenCount int    `json:"token_count"`
						}
						require.NoError(t, json.Unmarshal([]byte(count.stdout), &result))
						require.Equal(t, "o200k_base", result.Encoding)
						require.Equal(t, 13, result.InputBytes)
						require.Equal(t, 4, result.TokenCount)
					}
					guide := runMainDispatchWithEnv(t, "bash", env, "openai", "--format", format, "codex")
					require.Zero(t, guide.code, "%s", guide.stderr)
					require.Empty(t, guide.stderr)
					require.Contains(t, guide.stdout, "npm install -g @openai/codex")
					require.Contains(t, guide.stdout, "brew install --cask codex")
					if format == "json" {
						var result map[string]any
						require.NoError(t, json.Unmarshal([]byte(guide.stdout), &result))
						require.Equal(t, "codex", result["command"])
					}
					require.NotContains(t, count.stdout+guide.stdout, "synthetic-private-")
				})
			}
		})
	}
	require.Zero(t, requests.Load(), "local commands reached the API")
}

func TestMainLocalUtilitiesRootFlagPlacement(t *testing.T) {
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint")
	for _, argv := range [][]string{
		{"openai", "--format", "json", "tokenizer", "count", "--text", ""},
		{"openai", "tokenizer", "--format", "json", "count", "--text", ""},
		{"openai", "tokenizer", "count", "--text", "", "--format", "json"},
	} {
		t.Run(strings.Join(argv[1:], "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, argv...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.JSONEq(t, `{"encoding":"o200k_base","input_bytes":0,"token_count":0}`, got.stdout)
		})
	}
}

func TestMainLocalUtilitiesNamesDoNotBypassRemoteValidation(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	for _, configuration := range []struct {
		name, want string
		env        []string
	}{
		{"base URL", "OPENAI_BASE_URL", []string{"OPENAI_BASE_URL=synthetic-private-endpoint"}},
		{"mTLS", "mTLS client certificate and key files", []string{
			"OPENAI_BASE_URL=" + server.URL,
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert",
		}},
	} {
		for _, value := range []string{"tokenizer", "codex"} {
			t.Run(configuration.name+"/"+value, func(t *testing.T) {
				env := append(localUtilitiesEnvironment(t), configuration.env...)
				got := runMainDispatchWithEnv(t, "bash", env,
					"openai", "--organization", value, "responses", "input-tokens", "count",
					"--model", value, "--input", value)
				require.Equal(t, 1, got.code)
				require.Empty(t, got.stdout)
				require.Contains(t, got.stderr, configuration.want)
				require.NotContains(t, got.stderr, "synthetic-private-")
			})
		}
	}
	require.Zero(t, requests.Load(), "invalid remote configuration reached the API")
}

func TestMainLocalUtilitiesPreserveRemoteTokenCounter(t *testing.T) {
	const response = `{"object":"response.input_tokens","input_tokens":7,"synthetic_extra":"preserved"}`
	for _, route := range []struct {
		name, stdin, want string
		args              []string
	}{
		{"nested", "", `{"model":"codex","input":"tokenizer\nsynthetic"}`,
			[]string{"responses", "input-tokens", "count", "--model", "codex", "--input", `"tokenizer\nsynthetic"`}},
		{"legacy", "", `{"model":"tokenizer","input":"codex\nsynthetic"}`,
			[]string{"responses:input-tokens", "count", "--model", "tokenizer", "--input", `"codex\nsynthetic"`}},
		{"YAML scalar compatibility", "", `{"model":"codex","input":"tokenizer synthetic"}`,
			[]string{"responses", "input-tokens", "count", "--model", "codex", "--input", "tokenizer\nsynthetic"}},
		{"piped JSON", `{"model":"codex","input":"tokenizer\nsynthetic"}`,
			`{"model":"codex","input":"tokenizer\nsynthetic"}`, []string{"responses", "input-tokens", "count"}},
	} {
		t.Run(route.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/responses/input_tokens" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer sk-synthetic-local-utilities-test" {
					t.Error("remote counter lost its API credential")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				} else if !json.Valid(body) {
					t.Errorf("remote counter sent invalid JSON: %q", body)
				} else {
					var actual, expected any
					_ = json.Unmarshal(body, &actual)
					_ = json.Unmarshal([]byte(route.want), &expected)
					actualJSON, _ := json.Marshal(actual)
					expectedJSON, _ := json.Marshal(expected)
					if string(actualJSON) != string(expectedJSON) {
						t.Errorf("request body changed: got %s, want %s", actualJSON, expectedJSON)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response)
			}))
			t.Cleanup(server.Close)
			env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL="+server.URL+"/v1",
				"OPENAI_API_KEY=sk-synthetic-local-utilities-test")
			var stdin *os.File
			if route.stdin != "" {
				path := filepath.Join(t.TempDir(), "synthetic-request.json")
				require.NoError(t, os.WriteFile(path, []byte(route.stdin), 0o600))
				var err error
				stdin, err = os.Open(path)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stdin.Close()) })
			}
			args := append([]string{"openai", "--format", "json"}, route.args...)
			got := runMainDispatchWithStdin(t, "bash", env, stdin, args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.JSONEq(t, response, got.stdout)
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestMainLocalUtilitiesErrorsRemainLocal(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL="+server.URL)
	for _, test := range []struct {
		name, want string
		args       []string
	}{
		{"encoding", "encoding", []string{"tokenizer", "count", "--text", "synthetic-private-input", "--encoding", "synthetic-private-encoding"}},
		{"sources", "--text", []string{"tokenizer", "count", "--text", "synthetic-private-input", "--file", "/synthetic-private/missing-file"}},
		{"destination", "destination", []string{"codex", "--destination", "https://synthetic-private.invalid"}},
		{"open without destination", "--destination", []string{"codex", "--open"}},
		{"extra argument", "argument", []string{"codex", "synthetic-private-argument"}},
		{"unknown option", "--help", []string{"codex", "--synthetic-private-option=synthetic-private-value"}},
		{"unsupported format", "--format", []string{"codex", "--format", "yaml"}},
		{"malformed explicit option", "--base-url", []string{"codex", "--base-url", "synthetic-private-endpoint"}},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				args := append([]string{"openai", "--format-error", format}, test.args...)
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				require.NotZero(t, got.code)
				require.Empty(t, got.stdout)
				require.Contains(t, strings.ToLower(got.stderr), test.want)
				require.NotContains(t, got.stderr, "synthetic-private-")
				if format == "json" {
					payload := decodeMainStructuredError(t, format, got.stderr)
					require.NotContains(t, payload, "status_code")
				}
			})
		}
	}
	require.Zero(t, requests.Load(), "local validation reached the API")
}

func TestMainLocalUtilitiesHelpAndCompletion(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert", "OPENAI_CLI_COMPLETION_FILE_VALUES=1")
	for _, route := range []struct {
		path, flag string
	}{
		{"tokenizer count", "--text"}, {"tokenizer inspect", "--encoding"},
		{"tokenizer encodings", "encodings"}, {"tokenizer licenses", "licenses"}, {"codex", "--destination"},
	} {
		t.Run("help/"+route.path, func(t *testing.T) {
			for _, full := range []bool{false, true} {
				args := append([]string{"openai"}, strings.Fields(route.path)...)
				args = append(args, "--help")
				if full {
					args = append([]string{"openai", "help", "--all"}, strings.Fields(route.path)...)
				}
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "openai "+route.path)
				require.Contains(t, got.stdout, route.flag)
				require.NotContains(t, got.stdout, "Key setup:", "local help must not imply an API-key requirement")
			}
		})
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run("completion/"+style, func(t *testing.T) {
			for _, test := range []struct {
				args []string
				want string
				code int
			}{
				{[]string{"tok"}, "tokenizer", 0},
				{[]string{"cod"}, "codex", 0},
				{[]string{"tokenizer", "cou"}, "count", 0},
				{[]string{"tokenizer", "lic"}, "licenses", 0},
				{[]string{"tokenizer", "count", "--fi"}, "--file", 0},
				{[]string{"codex", "--dest"}, "--destination", 0},
				{[]string{"responses", "input-tokens", "cou"}, "count", 0},
				{[]string{"tokenizer", "count", "--file", "synthetic-"}, "", 10},
				{[]string{"tokenizer", "count", "--text", "cod"}, "", 11},
			} {
				got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, test.args...)...)
				require.Equal(t, test.code, got.code, "%v: %+v", test.args, got)
				require.Empty(t, got.stderr)
				if test.want == "" {
					require.Empty(t, got.stdout)
				} else {
					var names []string
					for _, record := range strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n") {
						if style == "zsh" {
							record, _, _ = strings.Cut(record, ":")
						} else if style == "fish" {
							record, _, _ = strings.Cut(record, "\t")
						}
						names = append(names, record)
					}
					require.Equal(t, []string{test.want}, names)
				}
			}
		})
	}
	got := runMainDispatchWithEnv(t, "bash", append(env, "OPENAI_BASE_URL="+server.URL),
		"openai", "codex", "--destination", "docs")
	require.Equal(t, mainDispatchResult{stdout: "https://learn.chatgpt.com/docs/cli\n"}, got)
	require.Zero(t, requests.Load())
}
