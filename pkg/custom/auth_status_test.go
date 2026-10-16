package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func authStatusTestRoot(out io.Writer) *cli.Command {
	root := &cli.Command{Name: "openai", Writer: out, ErrWriter: io.Discard, Reader: tokenizerRejectReader{},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"},
			&cli.BoolFlag{Name: "debug"}, NewRequestHeaderFlag(),
		},
	}
	for _, pair := range [][2]string{{"api-key", "OPENAI_API_KEY"}, {"admin-api-key", "OPENAI_ADMIN_KEY"},
		{"organization", "OPENAI_ORG_ID"}, {"project", "OPENAI_PROJECT_ID"}} {
		root.Flags = append(root.Flags, &requestflag.Flag[string]{Name: pair[0], Sources: cli.EnvVars(pair[1])})
	}
	configureRootRequestFlags(root)
	registerAuthCommands(root)
	ConfigureCommandErrors(root)
	return root
}

func authStatusTestEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_BASE_URL", "OPENAI_CUSTOM_HEADERS"} {
		// Empty is sufficient for ordinary request fields; an absent base URL selects the SDK default.
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func TestAuthStatusSelection(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		env                   map[string]string
		args                  []string
		api, admin            authStatusCredential
		organization, project authStatusValue
	}{
		{name: "unset", api: authStatusCredential{Type: "none", Source: "unset"}, admin: authStatusCredential{Type: "none", Source: "unset"}, organization: authStatusValue{Source: "unset"}, project: authStatusValue{Source: "unset"}},
		{name: "environment", env: map[string]string{"OPENAI_API_KEY": "synthetic-api-secret", "OPENAI_ADMIN_KEY": "synthetic-admin-secret", "OPENAI_ORG_ID": "org-env", "OPENAI_PROJECT_ID": "proj-env"},
			api: authStatusCredential{true, "api_key", "environment:OPENAI_API_KEY"}, admin: authStatusCredential{true, "admin_api_key", "environment:OPENAI_ADMIN_KEY"}, organization: authStatusValue{"org-env", "environment:OPENAI_ORG_ID"}, project: authStatusValue{"proj-env", "environment:OPENAI_PROJECT_ID"}},
		{name: "explicit empty suppresses environment", env: map[string]string{"OPENAI_API_KEY": "synthetic-api-secret", "OPENAI_ADMIN_KEY": "synthetic-admin-secret", "OPENAI_ORG_ID": "org-env", "OPENAI_PROJECT_ID": "proj-env"},
			args: []string{"--api-key=", "--admin-api-key=", "--organization=", "--project="},
			api:  authStatusCredential{false, "none", "flag:--api-key"}, admin: authStatusCredential{false, "none", "flag:--admin-api-key"}, organization: authStatusValue{"", "flag:--organization"}, project: authStatusValue{"", "flag:--project"}},
		{name: "environment header", env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Ignored line\nAuthorization: Bearer synthetic-header-secret\nOpenAI-Organization: org-header\nOpenAI-Project: proj-header"},
			api: authStatusCredential{true, "authorization_header", "environment:OPENAI_CUSTOM_HEADERS"}, admin: authStatusCredential{true, "authorization_header", "environment:OPENAI_CUSTOM_HEADERS"}, organization: authStatusValue{"org-header", "environment:OPENAI_CUSTOM_HEADERS"}, project: authStatusValue{"proj-header", "environment:OPENAI_CUSTOM_HEADERS"}},
		{name: "empty key preserves environment header", env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-header-secret"}, args: []string{"--api-key=", "--admin-api-key="},
			api: authStatusCredential{true, "authorization_header", "environment:OPENAI_CUSTOM_HEADERS"}, admin: authStatusCredential{true, "authorization_header", "environment:OPENAI_CUSTOM_HEADERS"}, organization: authStatusValue{Source: "unset"}, project: authStatusValue{Source: "unset"}},
		{name: "keys and context override environment headers", env: map[string]string{"OPENAI_API_KEY": "synthetic-api-secret", "OPENAI_ADMIN_KEY": "synthetic-admin-secret", "OPENAI_ORG_ID": "", "OPENAI_PROJECT_ID": "", "OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-header-secret\nOpenAI-Organization: org-header\nOpenAI-Project: proj-header"},
			api: authStatusCredential{true, "api_key", "environment:OPENAI_API_KEY"}, admin: authStatusCredential{true, "admin_api_key", "environment:OPENAI_ADMIN_KEY"}, organization: authStatusValue{"", "environment:OPENAI_ORG_ID"}, project: authStatusValue{"", "environment:OPENAI_PROJECT_ID"}},
		{name: "literal empty authorization", env: map[string]string{"OPENAI_API_KEY": "synthetic-api-secret", "OPENAI_ADMIN_KEY": "synthetic-admin-secret"}, args: []string{"-H", "Authorization:"},
			api: authStatusCredential{false, "none", "flag:--header"}, admin: authStatusCredential{false, "none", "flag:--header"}, organization: authStatusValue{Source: "unset"}, project: authStatusValue{Source: "unset"}},
		{name: "last literal headers win", args: []string{"-H", "Authorization: Bearer synthetic-first-secret", "-H", "authorization: Bearer synthetic-last-secret", "--project=proj-flag", "-H", "OpenAI-Project: proj-first", "-H", "openai-project: proj-last"},
			api: authStatusCredential{true, "authorization_header", "flag:--header"}, admin: authStatusCredential{true, "authorization_header", "flag:--header"}, organization: authStatusValue{Source: "unset"}, project: authStatusValue{"proj-last", "flag:--header"}},
		{name: "URL Basic fallback", env: map[string]string{"OPENAI_BASE_URL": "https://synthetic-user:synthetic-password@example.invalid/v1/"}, args: []string{"-H", "Authorization:"},
			api: authStatusCredential{true, "url_basic_auth", "environment:OPENAI_BASE_URL"}, admin: authStatusCredential{true, "url_basic_auth", "environment:OPENAI_BASE_URL"}, organization: authStatusValue{Source: "unset"}, project: authStatusValue{Source: "unset"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authStatusTestEnvironment(t, tc.env)
			for _, position := range []int{0, 1, 2} {
				var out bytes.Buffer
				root := authStatusTestRoot(&out)
				route := []string{"auth", "status"}
				args := append([]string{"openai", "--format=json"}, route[:position]...)
				args = append(args, tc.args...)
				args = append(args, route[position:]...)
				require.NoError(t, root.Run(t.Context(), args), "position %d", position)
				var got authStatusReport
				require.NoError(t, json.Unmarshal(out.Bytes(), &got))
				require.True(t, got.Offline)
				require.False(t, got.Verified)
				require.Equal(t, tc.api, got.API)
				require.Equal(t, tc.admin, got.Admin)
				require.Equal(t, tc.organization, got.Organization)
				require.Equal(t, tc.project, got.Project)
				require.NotContains(t, out.String(), "synthetic-")
			}
		})
	}
}

func TestAuthStatusEndpointAndSingleEnvironmentSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		args    []string
		want    authStatusEndpoint
		invalid bool
	}{
		{name: "default", want: authStatusEndpoint{authStatusValue: authStatusValue{"https://api.openai.com/v1/", "default"}}},
		{name: "empty environment", env: map[string]string{"OPENAI_BASE_URL": ""}, want: authStatusEndpoint{authStatusValue: authStatusValue{"", "environment:OPENAI_BASE_URL"}}},
		{name: "empty flag falls back", env: map[string]string{"OPENAI_BASE_URL": "https://env.invalid/v1"}, args: []string{"--base-url="}, want: authStatusEndpoint{authStatusValue: authStatusValue{"https://env.invalid/v1", "environment:OPENAI_BASE_URL"}}},
		{name: "override malformed environment", env: map[string]string{"OPENAI_BASE_URL": "https://%synthetic-secret"}, args: []string{"--base-url=https://flag.invalid/"}, want: authStatusEndpoint{authStatusValue: authStatusValue{"https://flag.invalid/", "flag:--base-url"}}},
		{name: "private URL components", env: map[string]string{"OPENAI_BASE_URL": "https://synthetic-user:synthetic-password@example.invalid/synthetic-path?token=synthetic-query#synthetic-fragment"}, want: authStatusEndpoint{authStatusValue: authStatusValue{"https://example.invalid/", "environment:OPENAI_BASE_URL"}, Redacted: true}},
		{name: "malformed URL", env: map[string]string{"OPENAI_BASE_URL": "https://%synthetic-secret"}, invalid: true},
		{name: "missing scheme", env: map[string]string{"OPENAI_BASE_URL": "synthetic-secret"}, invalid: true},
		{name: "control character", env: map[string]string{"OPENAI_BASE_URL": "https://example.invalid/synthetic-secret\x1b"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authStatusTestEnvironment(t, tc.env)
			root := authStatusTestRoot(io.Discard)
			calls := map[string]int{}
			root.Command("auth").Command("status").Action = func(_ context.Context, command *cli.Command) error {
				result, err := resolveAuthStatus(command.Root(), func(name string) (string, bool) { calls[name]++; value, ok := tc.env[name]; return value, ok })
				if tc.invalid {
					require.Error(t, err)
					require.NotContains(t, err.Error(), "synthetic-secret")
				} else {
					require.NoError(t, err)
					require.Equal(t, tc.want, result.BaseURL)
				}
				return nil
			}
			require.NoError(t, root.Run(t.Context(), append([]string{"openai", "auth", "status"}, tc.args...)))
			require.Len(t, calls, 6)
			for _, count := range calls {
				require.Equal(t, 1, count)
			}
		})
	}
}

func TestAuthStatusOutputAndLocalErrors(t *testing.T) {
	authStatusTestEnvironment(t, map[string]string{"OPENAI_PROJECT_ID": "proj\x1b[31m\nspoof\tcolumn"})
	for _, format := range []string{"auto", "text", "json"} {
		var out bytes.Buffer
		root := authStatusTestRoot(&out)
		require.NoError(t, root.Run(t.Context(), []string{"openai", "auth", "status", "--format", format}))
		require.NotContains(t, out.String(), "\x1b")
		if format == "json" {
			var report authStatusReport
			require.NoError(t, json.Unmarshal(out.Bytes(), &report))
			require.Equal(t, "proj\x1b[31m\nspoof\tcolumn", report.Project.Value)
		} else {
			require.Contains(t, out.String(), "offline; not verified")
			require.NotContains(t, out.String(), "\nspoof")
			require.NotContains(t, out.String(), "\t")
			require.Contains(t, out.String(), `\nspoof\tcolumn`)
		}
	}
	for _, args := range [][]string{{"extra"}, {"--format=jsonl"}, {"--transform=project"}, {"--raw-output=false"}, {"-H", "synthetic-secret"}, {"-H", "Authorization: synthetic-secret\r"}} {
		var out bytes.Buffer
		err := authStatusTestRoot(&out).Run(t.Context(), append([]string{"openai", "auth", "status"}, args...))
		require.Error(t, err)
		require.Empty(t, out.String())
		require.NotContains(t, err.Error(), "synthetic-secret")
	}
	failure := errors.New("synthetic writer failure")
	root := authStatusTestRoot(authStatusFailWriter{err: failure})
	require.ErrorIs(t, root.Run(t.Context(), []string{"openai", "auth", "status"}), failure)
	root = authStatusTestRoot(authStatusFailWriter{})
	require.ErrorIs(t, root.Run(t.Context(), []string{"openai", "auth", "status", "--format=json"}), io.ErrShortWrite)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, handleAuthStatus(ctx, authStatusTestRoot(io.Discard).Command("auth").Command("status")), context.Canceled)
}

type authStatusFailWriter struct{ err error }

func (w authStatusFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestAuthStatusMatchesSDKAuthorizationSelection(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		env                      map[string]string
		args                     []string
		api, admin, org, project string
	}{
		{name: "separate keys", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin"}, api: "Bearer synthetic-api", admin: "Bearer synthetic-admin"},
		{name: "explicit empty key retains header", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-header"}, args: []string{"--api-key="}, api: "Bearer synthetic-header", admin: "Bearer synthetic-header"},
		{name: "literal empty header", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin"}, args: []string{"-H", "Authorization:"}},
		{name: "empty context overrides headers", env: map[string]string{"OPENAI_ORG_ID": "", "OPENAI_PROJECT_ID": "", "OPENAI_CUSTOM_HEADERS": "OpenAI-Organization: org-header\nOpenAI-Project: proj-header"}},
		{name: "literal overrides", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin", "OPENAI_PROJECT_ID": "proj-env"}, args: []string{"-H", "Authorization: Bearer synthetic-literal", "-H", "OpenAI-Project: proj-literal"}, api: "Bearer synthetic-literal", admin: "Bearer synthetic-literal", project: "proj-literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authStatusTestEnvironment(t, tc.env)
			var received []http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received = append(received, r.Header.Clone())
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[],"object":"list","has_more":false}`)
			}))
			defer server.Close()
			for _, admin := range []bool{false, true} {
				root := authStatusTestRoot(io.Discard)
				root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, c *cli.Command) error {
					options := GetDefaultRequestOptions(c)
					headers, err := requestHeaders(c)
					if err != nil {
						return err
					}
					for name, values := range headers {
						options = append(options, option.WithHeader(name, values[0]))
					}
					client := openai.NewClient(options...)
					if admin {
						_, err = client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{})
					} else {
						_, err = client.Models.List(ctx)
					}
					return err
				}})
				args := append([]string{"openai", "--base-url", server.URL, "probe"}, tc.args...)
				require.NoError(t, root.Run(t.Context(), args))
			}
			require.Len(t, received, 2)
			require.Equal(t, tc.api, received[0].Get("Authorization"))
			require.Equal(t, tc.admin, received[1].Get("Authorization"))
			for _, header := range received {
				require.Equal(t, tc.org, header.Get("OpenAI-Organization"))
				require.Equal(t, tc.project, header.Get("OpenAI-Project"))
			}
		})
	}
}

type authStatusRoundTripFunc func(*http.Request) (*http.Response, error)

func (f authStatusRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func authStatusTestTransport(t *testing.T, roundTrip authStatusRoundTripFunc) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = roundTrip
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func authStatusRunJSON(t *testing.T, ctx context.Context, args ...string) (map[string]any, string, error) {
	t.Helper()
	var out bytes.Buffer
	err := authStatusTestRoot(&out).Run(ctx, append([]string{"openai", "auth", "status", "--format=json"}, args...))
	var report map[string]any
	if out.Len() > 0 {
		require.NoError(t, json.Unmarshal(out.Bytes(), &report), "status output must remain valid JSON")
	}
	return report, out.String(), err
}

func authStatusUnknownIdentity(t *testing.T, report map[string]any, kind, reason string) {
	t.Helper()
	identity, ok := report[kind+"_identity"].(map[string]any)
	require.True(t, ok, "missing %s identity", kind)
	require.Equal(t, "unknown", identity["status"])
	require.IsType(t, "", identity["reason"])
	require.NotEmpty(t, identity["reason"])
	if reason != "" {
		require.Equal(t, reason, identity["reason"])
	}
	require.Nil(t, identity["name"])
	require.Nil(t, identity["email"])
	require.Len(t, identity, 4, "unknown identity must not claim a provider or principal ID")
}

func TestAuthStatusIdentityOfflineAndFlagErrors(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "not configured", true: "configured"}[configured], func(t *testing.T) {
			env := map[string]string{}
			reason := "not_configured"
			if configured {
				env = map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin"}
				reason = "not_requested"
			}
			authStatusTestEnvironment(t, env)
			calls := 0
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected transport call")
			})
			report, _, err := authStatusRunJSON(t, t.Context())
			require.NoError(t, err)
			require.Equal(t, true, report["offline"])
			require.Equal(t, false, report["verified"])
			require.NotContains(t, report, "verified_credential_kind")
			authStatusUnknownIdentity(t, report, "api", reason)
			authStatusUnknownIdentity(t, report, "admin", reason)
			for _, args := range [][]string{{"--verify"}, {"--credential-kind=api"}, {"--credential-kind=admin"}, {"--verify", "--credential-kind=other"}} {
				_, output, err := authStatusRunJSON(t, t.Context(), args...)
				require.Error(t, err)
				require.Empty(t, output)
			}
			require.Zero(t, calls)
		})
	}
}

func TestAuthStatusIdentitySelectedCredentialAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, kind, baseURL, authorization string
		args                               []string
	}{
		{name: "API default", kind: "api", authorization: "Bearer synthetic-api"},
		{name: "Admin default", kind: "admin", authorization: "Bearer synthetic-admin"},
		{name: "without trailing slash", kind: "api", baseURL: "https://api.openai.com/v1", authorization: "Bearer synthetic-api"},
		{name: "explicit default port", kind: "admin", baseURL: "https://api.openai.com:443/v1/", authorization: "Bearer synthetic-admin"},
		{name: "environment header fallback", kind: "api", authorization: "Bearer synthetic-environment-header", args: []string{"--api-key="}},
		{name: "literal header wins", kind: "admin", authorization: "Bearer synthetic-last", args: []string{"-H", "Authorization: Bearer synthetic-first", "-H", "authorization: Bearer synthetic-last"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authStatusTestEnvironment(t, map[string]string{
				"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin",
				"OPENAI_ORG_ID": "org-environment", "OPENAI_PROJECT_ID": "proj-environment",
				"OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-environment-header\nCookie: synthetic-cookie\nX-Synthetic-Ignored: synthetic-header",
			})
			calls := 0
			body := &authStatusCloseBody{Reader: strings.NewReader(`{"object":"user","id":"user-synthetic","name":"Synthetic User","email":"synthetic@example.invalid","orgs":{"data":[{"id":"org-unrelated"}]}}`)}
			authStatusTestTransport(t, func(request *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodGet, request.Method)
				require.Equal(t, "https", request.URL.Scheme)
				require.Equal(t, "api.openai.com", request.URL.Hostname())
				require.Equal(t, "/v1/me", request.URL.Path)
				require.Empty(t, request.URL.RawQuery)
				require.Nil(t, request.Body)
				require.Equal(t, http.Header{
					"Accept": {"application/json"}, "Authorization": {tc.authorization},
					"Openai-Organization": {"org-selected"}, "Openai-Project": {"proj-selected"},
				}, request.Header)
				deadline, ok := request.Context().Deadline()
				require.True(t, ok, "identity lookup must have a finite deadline")
				require.Positive(t, time.Until(deadline))
				require.LessOrEqual(t, time.Until(deadline), 15*time.Second)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
			})
			args := []string{"--verify", "--credential-kind=" + tc.kind, "--organization=org-selected", "-H", "OpenAI-Project: proj-selected", "-H", "X-Do-Not-Forward: synthetic-extra"}
			if tc.baseURL != "" {
				args = append(args, "--base-url="+tc.baseURL)
			}
			args = append(args, tc.args...)
			report, output, err := authStatusRunJSON(t, t.Context(), args...)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.True(t, body.closed)
			require.Equal(t, false, report["offline"])
			require.Equal(t, true, report["verified"])
			require.Equal(t, tc.kind, report["verified_credential_kind"])
			require.Equal(t, map[string]any{"status": "resolved", "provider": "openai_me_v1", "id": "user-synthetic", "name": "Synthetic User", "email": "synthetic@example.invalid"}, report[tc.kind+"_identity"])
			other := map[string]string{"api": "admin", "admin": "api"}[tc.kind]
			authStatusUnknownIdentity(t, report, other, "not_requested")
			for _, secret := range []string{"synthetic-api", "synthetic-admin", "synthetic-environment-header", "synthetic-first", "synthetic-last", "synthetic-cookie", "synthetic-extra"} {
				require.NotContains(t, output, secret)
			}
		})
	}
}

type authStatusCloseBody struct {
	*strings.Reader
	closed bool
}

func (body *authStatusCloseBody) Close() error { body.closed = true; return nil }

func TestAuthStatusIdentityUnsupportedProviderAndMissingCredential(t *testing.T) {
	for _, baseURL := range []string{
		"http://api.openai.com/v1", "https://example.invalid/v1", "https://api.openai.com.example.invalid/v1",
		"https://api.openai.com./v1", "https://api.openai.com:444/v1", "https://api.openai.com/", "https://api.openai.com/v1/extra",
		"https://api.openai.com/v1?token=synthetic-query", "https://api.openai.com/v1?", "https://api.openai.com/v1#synthetic-fragment",
		"https://synthetic-user:synthetic-password@api.openai.com/v1", "https://api.openai.com/v%31",
	} {
		t.Run(baseURL, func(t *testing.T) {
			authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
			calls := 0
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected transport call")
			})
			report, output, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api", "--base-url="+baseURL)
			require.Error(t, err)
			require.Zero(t, calls)
			authStatusUnknownIdentity(t, report, "api", "unsupported_provider")
			require.Equal(t, false, report["verified"])
			require.NotContains(t, report, "verified_credential_kind")
			for _, secret := range []string{"synthetic-api", "synthetic-query", "synthetic-fragment", "synthetic-user", "synthetic-password"} {
				require.NotContains(t, output+err.Error(), secret)
			}
		})
	}
	for _, kind := range []string{"api", "admin"} {
		t.Run("missing "+kind, func(t *testing.T) {
			authStatusTestEnvironment(t, nil)
			calls := 0
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected transport call")
			})
			report, _, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind="+kind)
			require.Error(t, err)
			require.Zero(t, calls)
			authStatusUnknownIdentity(t, report, kind, "not_configured")
		})
	}
}

func TestAuthStatusIdentityRejectsInvalidOrReflectedResponse(t *testing.T) {
	for _, body := range []string{
		`{`, `[]`, `null`, `{}`, `{"id":"user-synthetic"}`, `{"object":"organization","id":"org-synthetic"}`,
		`{"object":"user","id":null}`, `{"object":"user","id":123}`, `{"object":"user","id":""}`,
		`{"object":"user","id":"  "}`, `{"object":"user","user":{"id":"user-nested"}}`,
		`{"object":"user","id":"user-synthetic","name":42}`, `{"object":"user","id":"user-synthetic","email":[]}`,
		`{"object":"user","id":"user-one","id":"user-two"}`, `{"object":"user","id":"user-synthetic","name":"one","name":"two"}`,
		`{"object":"user","id":"user-synthetic","email":null,"email":"synthetic@example.invalid"}`,
		`{"object":"user","object":"user","id":"user-synthetic"}`,
		`{"object":"user","id":"user-synthetic","ignored":1,"ignored":2}`,
		`{"object":"user","id":"user-synthetic"} {"id":"user-trailing"}`, `{"object":"user","id":"user-synthetic"} trailing`,
		`{"object":"user","id":"user-synthetic-api"}`, `{"object":"user","id":"user-synthetic","name":"synthetic-admin"}`,
		`{"object":"user","id":"user-synthetic","email":"synthetic-api@example.invalid"}`,
	} {
		t.Run(body, func(t *testing.T) {
			authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin"})
			calls := 0
			responseBody := &authStatusCloseBody{Reader: strings.NewReader(body)}
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: responseBody}, nil
			})
			report, output, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api")
			require.Error(t, err)
			require.Equal(t, 1, calls)
			require.True(t, responseBody.closed)
			authStatusUnknownIdentity(t, report, "api", "invalid_response")
			authStatusUnknownIdentity(t, report, "admin", "not_requested")
			require.Equal(t, false, report["verified"])
			require.NotContains(t, report, "verified_credential_kind")
			require.NotContains(t, output+err.Error(), "synthetic-api")
			require.NotContains(t, output+err.Error(), "synthetic-admin")
		})
	}
}

func TestAuthStatusIdentityStatusErrorsAndRedirects(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
			calls := 0
			body := &authStatusCloseBody{Reader: strings.NewReader(`{"error":"synthetic-api"}`)}
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://example.invalid/synthetic-api"}}, Body: body}, nil
			})
			report, output, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api")
			require.Error(t, err)
			require.Equal(t, 1, calls, "identity lookup must not redirect or retry")
			require.True(t, body.closed)
			authStatusUnknownIdentity(t, report, "api", "")
			require.NotContains(t, output+err.Error(), "synthetic-api")
			require.NotContains(t, output+err.Error(), "example.invalid")
		})
	}
}

func TestAuthStatusIdentityOptionalFieldsAndLargeIgnoredData(t *testing.T) {
	for _, body := range []string{
		`{"object":"user","id":"user-synthetic"}`,
		`{"object":"user","id":"user-synthetic","name":null,"email":null}`,
		`{"object":"user","id":"user-synthetic","ignored":"` + strings.Repeat("x", 2*1024*1024) + `","orgs":{"data":[{"id":"org-other"}]}}`,
	} {
		authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
		authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		report, output, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api")
		require.NoError(t, err)
		require.Equal(t, map[string]any{"status": "resolved", "provider": "openai_me_v1", "id": "user-synthetic", "name": nil, "email": nil}, report["api_identity"])
		require.NotContains(t, output, "org-other")
		require.Less(t, len(output), 4096)
	}
}

func TestAuthStatusIdentityTransportFailureAndCancellation(t *testing.T) {
	t.Run("transport error stays private", func(t *testing.T) {
		authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
		calls := 0
		authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("synthetic-api credential reflected by transport")
		})
		report, output, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api")
		require.Error(t, err)
		require.Equal(t, 1, calls)
		authStatusUnknownIdentity(t, report, "api", "")
		require.NotContains(t, output+err.Error(), "synthetic-api")
	})
	t.Run("in flight cancellation", func(t *testing.T) {
		authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		authStatusTestTransport(t, func(request *http.Request) (*http.Response, error) {
			calls++
			cancel()
			<-request.Context().Done()
			return nil, request.Context().Err()
		})
		_, output, err := authStatusRunJSON(t, ctx, "--verify", "--credential-kind=api")
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, calls)
		require.NotContains(t, output+err.Error(), "synthetic-api")
	})
}

func TestAuthStatusIdentityRejectsNonBearerCredential(t *testing.T) {
	for _, authorization := range []string{"Basic synthetic-basic", "Bearer ", "synthetic-api"} {
		t.Run(authorization, func(t *testing.T) {
			authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
			calls := 0
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected transport call")
			})
			report, output, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api", "-H", "Authorization: "+authorization)
			require.Error(t, err)
			require.Zero(t, calls)
			authStatusUnknownIdentity(t, report, "api", "invalid_configuration")
			require.NotContains(t, output+err.Error(), "synthetic-basic")
			require.NotContains(t, output+err.Error(), "synthetic-api")
		})
	}
}

func TestAuthStatusIdentityRequiresJSONContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/html", "text/plain", "application/json; charset=\"unterminated"} {
		t.Run(contentType, func(t *testing.T) {
			authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
			body := &authStatusCloseBody{Reader: strings.NewReader(`{"object":"user","id":"user-synthetic"}`)}
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: body}, nil
			})
			report, _, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api")
			require.Error(t, err)
			require.True(t, body.closed)
			authStatusUnknownIdentity(t, report, "api", "invalid_response")
		})
	}
}

func TestAuthStatusIdentityRepeatedCallsUseCurrentCredential(t *testing.T) {
	authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-first"})
	var authorization []string
	var bodies []*authStatusCloseBody
	authStatusTestTransport(t, func(request *http.Request) (*http.Response, error) {
		authorization = append(authorization, request.Header.Get("Authorization"))
		id := "user-first"
		if len(authorization) == 2 {
			id = "user-second"
		}
		body := &authStatusCloseBody{Reader: strings.NewReader(`{"object":"user","id":"` + id + `"}`)}
		bodies = append(bodies, body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: body}, nil
	})
	first, _, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api")
	require.NoError(t, err)
	t.Setenv("OPENAI_API_KEY", "synthetic-second")
	second, _, err := authStatusRunJSON(t, t.Context(), "--verify", "--credential-kind=api")
	require.NoError(t, err)
	require.Equal(t, []string{"Bearer synthetic-first", "Bearer synthetic-second"}, authorization)
	require.Equal(t, "user-first", first["api_identity"].(map[string]any)["id"])
	require.Equal(t, "user-second", second["api_identity"].(map[string]any)["id"])
	for _, body := range bodies {
		require.True(t, body.closed)
	}
}

type authStatusDeadlineBody struct {
	ctx    context.Context
	closed bool
}

func (body *authStatusDeadlineBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (body *authStatusDeadlineBody) Close() error { body.closed = true; return nil }

func TestAuthStatusIdentityParentDeadline(t *testing.T) {
	authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	calls := 0
	var body *authStatusDeadlineBody
	authStatusTestTransport(t, func(request *http.Request) (*http.Response, error) {
		calls++
		body = &authStatusDeadlineBody{ctx: request.Context()}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
	})
	_, output, err := authStatusRunJSON(t, ctx, "--verify", "--credential-kind=api")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	require.True(t, body.closed)
	require.NotContains(t, output+err.Error(), "synthetic-api")
	require.NotContains(t, output, `"status":"resolved"`)
}

func TestAuthStatusIdentityOutputFailurePreservesLookupFailure(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api"})
			body := &authStatusCloseBody{Reader: strings.NewReader(`{"object":"user","id":"user-synthetic"}`)}
			authStatusTestTransport(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
			})
			root := authStatusTestRoot(authStatusFailWriter{err: syscall.EPIPE})
			err := root.Run(t.Context(), []string{"openai", "auth", "status", "--format=json", "--verify", "--credential-kind=api"})
			require.ErrorIs(t, err, syscall.EPIPE)
			require.Equal(t, status == http.StatusOK, isOutputBrokenPipe(err), "lookup failure must prevent broken-pipe suppression")
			require.True(t, body.closed)
			require.NotContains(t, err.Error(), "synthetic-api")
		})
	}
}

func TestAuthStatusIdentityCommandReuseClearsVerification(t *testing.T) {
	authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin"})
	var output bytes.Buffer
	root := authStatusTestRoot(&output)
	var authorization []string
	authStatusTestTransport(t, func(request *http.Request) (*http.Response, error) {
		authorization = append(authorization, request.Header.Get("Authorization"))
		id := "user-api"
		if request.Header.Get("Authorization") == "Bearer synthetic-admin" {
			id = "user-admin"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"object":"user","id":"` + id + `"}`))}, nil
	})
	for _, tc := range []struct {
		kind  string
		calls int
	}{{"api", 1}, {"", 1}, {"admin", 2}} {
		output.Reset()
		args := []string{"openai", "auth", "status", "--format=json"}
		if tc.kind != "" {
			args = append(args, "--verify", "--credential-kind="+tc.kind)
		}
		require.NoError(t, root.Run(t.Context(), args))
		var report map[string]any
		require.NoError(t, json.Unmarshal(output.Bytes(), &report))
		require.Len(t, authorization, tc.calls)
		if tc.kind == "" {
			require.Equal(t, true, report["offline"])
			require.Equal(t, false, report["verified"])
			require.NotContains(t, report, "verified_credential_kind")
			authStatusUnknownIdentity(t, report, "api", "not_requested")
			authStatusUnknownIdentity(t, report, "admin", "not_requested")
		} else {
			require.Equal(t, false, report["offline"])
			require.Equal(t, true, report["verified"])
			require.Equal(t, tc.kind, report["verified_credential_kind"])
			require.Equal(t, "user-"+tc.kind, report[tc.kind+"_identity"].(map[string]any)["id"])
		}
	}
	require.Equal(t, []string{"Bearer synthetic-api", "Bearer synthetic-admin"}, authorization)
}
