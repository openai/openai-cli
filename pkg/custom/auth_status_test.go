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
	"testing"

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
