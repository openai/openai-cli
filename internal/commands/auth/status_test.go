package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type rejectReader struct{}

func (rejectReader) Read([]byte) (int, error) { panic("auth status read stdin") }

func authStatusTestRoot(out io.Writer) *cli.Command {
	root := &cli.Command{Name: "openai", Writer: out, ErrWriter: io.Discard, Reader: rejectReader{},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"},
			&cli.BoolFlag{Name: "debug"}, request.NewRequestHeaderFlag(),
		},
	}
	for _, pair := range [][2]string{{"api-key", "OPENAI_API_KEY"}, {"admin-api-key", "OPENAI_ADMIN_KEY"},
		{"organization", "OPENAI_ORG_ID"}, {"project", "OPENAI_PROJECT_ID"}} {
		root.Flags = append(root.Flags, &requestflag.Flag[string]{Name: pair[0], Sources: cli.EnvVars(pair[1])})
	}
	request.ConfigureRootFlags(root)
	Register(root)
	return root
}

func TestRegisterPreservesAuthCommands(t *testing.T) {
	sibling := &cli.Command{Name: "existing"}
	group := &cli.Command{Name: "auth", Commands: []*cli.Command{sibling}}
	root := &cli.Command{Commands: []*cli.Command{group}}
	Register(root)
	status := group.Command("status")
	require.NotNil(t, status)
	require.Equal(t, true, status.Metadata[commands.LocalUtilityMetadata])
	Register(root)
	require.Equal(t, []*cli.Command{group}, root.Commands)
	require.Equal(t, []*cli.Command{sibling, status}, group.Commands)
}

func authStatusTestEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_BASE_URL", "OPENAI_CUSTOM_HEADERS", "OPENAI_WEBHOOK_SECRET"} {
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

func TestAuthStatusRepeatedRunClearsExplicitProject(t *testing.T) {
	authStatusTestEnvironment(t, nil)
	var out bytes.Buffer
	root := authStatusTestRoot(&out)
	args := []string{"openai", "auth", "status", "--format=json"}
	require.NoError(t, root.Run(t.Context(), append(args, "--project=proj-first")))
	out.Reset()
	require.NoError(t, root.Run(t.Context(), args))
	var status authStatusReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &status))
	require.Equal(t, authStatusValue{Source: "unset"}, status.Project)
}

func TestAuthStatusUsesParsedRequestValues(t *testing.T) {
	authStatusTestEnvironment(t, map[string]string{"OPENAI_API_KEY": "synthetic-key", "OPENAI_PROJECT_ID": "proj-selected"})
	var out bytes.Buffer
	root := authStatusTestRoot(&out)
	root.Before = func(ctx context.Context, _ *cli.Command) (context.Context, error) {
		t.Setenv("OPENAI_API_KEY", "")
		t.Setenv("OPENAI_PROJECT_ID", "proj-later")
		return ctx, nil
	}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "auth", "status", "--format=json"}))
	var status authStatusReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &status))
	require.Equal(t, authStatusCredential{true, "api_key", "environment:OPENAI_API_KEY"}, status.API)
	require.Equal(t, authStatusValue{"proj-selected", "environment:OPENAI_PROJECT_ID"}, status.Project)
}

func TestAuthStatusEndpointAndSingleEnvironmentSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		args    []string
		want    authStatusEndpoint
		invalid string
	}{
		{name: "default", want: authStatusEndpoint{authStatusValue: authStatusValue{"https://api.openai.com/v1/", "default"}}},
		{name: "empty environment", env: map[string]string{"OPENAI_BASE_URL": ""}, want: authStatusEndpoint{authStatusValue: authStatusValue{"", "environment:OPENAI_BASE_URL"}}},
		{name: "empty flag falls back", env: map[string]string{"OPENAI_BASE_URL": "https://env.invalid/v1"}, args: []string{"--base-url="}, want: authStatusEndpoint{authStatusValue: authStatusValue{"https://env.invalid/v1", "environment:OPENAI_BASE_URL"}}},
		{name: "override malformed environment", env: map[string]string{"OPENAI_BASE_URL": "https://%synthetic-secret"}, args: []string{"--base-url=https://flag.invalid/"}, want: authStatusEndpoint{authStatusValue: authStatusValue{"https://flag.invalid/", "flag:--base-url"}}},
		{name: "private URL components", env: map[string]string{"OPENAI_BASE_URL": "https://synthetic-user:synthetic-password@example.invalid/synthetic-path?token=synthetic-query#synthetic-fragment"}, want: authStatusEndpoint{authStatusValue: authStatusValue{"https://example.invalid/", "environment:OPENAI_BASE_URL"}, Redacted: true}},
		{name: "IPv6 loopback", args: []string{"--base-url=http://[::1]:8080/v1/"}, want: authStatusEndpoint{authStatusValue: authStatusValue{"http://[::1]:8080/v1/", "flag:--base-url"}}},
		{name: "IPv4 loopback", env: map[string]string{"OPENAI_BASE_URL": "http://127.255.255.254/v1"}, want: authStatusEndpoint{authStatusValue: authStatusValue{"http://127.255.255.254/v1", "environment:OPENAI_BASE_URL"}}},
		{name: "encoded API path is custom", env: map[string]string{"OPENAI_BASE_URL": "https://example.invalid/v1%2F"},
			want: authStatusEndpoint{authStatusValue{"https://example.invalid/", "environment:OPENAI_BASE_URL"}, true}},
		{name: "invalid port", env: map[string]string{"OPENAI_BASE_URL": "http://localhost:65536/v1"}, invalid: "OPENAI_BASE_URL must be an absolute HTTP(S) URL with a valid host"},
		{name: "remote HTTP", env: map[string]string{"OPENAI_BASE_URL": "http://example.invalid/synthetic-secret"}, invalid: "OPENAI_BASE_URL must use HTTPS"},
		{name: "remote HTTP flag", args: []string{"--base-url=http://localhost@synthetic-secret.invalid/v1"}, invalid: "--base-url must use HTTPS"},
		{name: "missing host", env: map[string]string{"OPENAI_BASE_URL": "https:///synthetic-secret"}, invalid: "OPENAI_BASE_URL must be an absolute HTTP(S) URL with a valid host"},
		{name: "empty hostname", args: []string{"--base-url=https://synthetic-secret@:443/v1"}, invalid: "--base-url must be an absolute HTTP(S) URL with a valid host"},
		{name: "malformed URL", env: map[string]string{"OPENAI_BASE_URL": "https://%synthetic-secret"}, invalid: "OPENAI_BASE_URL must be an absolute HTTP(S) URL with a valid host"},
		{name: "missing scheme", env: map[string]string{"OPENAI_BASE_URL": "synthetic-secret"}, invalid: "OPENAI_BASE_URL must start with http:// or https://"},
		{name: "control character", env: map[string]string{"OPENAI_BASE_URL": "https://example.invalid/synthetic-secret\x1b"}, invalid: "OPENAI_BASE_URL must be an absolute HTTP(S) URL with a valid host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authStatusTestEnvironment(t, tc.env)
			root := authStatusTestRoot(io.Discard)
			calls := map[string]int{}
			root.Command("auth").Command("status").Action = func(ctx context.Context, command *cli.Command) error {
				result, err := resolveAuthStatus(ctx, command.Root(), func(name string) (string, bool) {
					calls[name]++
					value, ok := tc.env[name]
					return value, ok
				})
				if tc.invalid != "" {
					require.ErrorContains(t, err, tc.invalid)
					require.NotContains(t, err.Error(), "synthetic-secret")
				} else {
					require.NoError(t, err)
					require.Equal(t, tc.want, result.BaseURL)
				}
				return nil
			}
			require.NoError(t, root.Run(t.Context(), append([]string{"openai", "auth", "status"}, tc.args...)))
			require.Len(t, calls, 8)
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
	t.Run("positional recovery uses invoked executable", func(t *testing.T) {
		root := authStatusTestRoot(io.Discard)
		root.Metadata = map[string]any{"help-invocation": "'/my CLI/openai'"}
		err := root.Run(t.Context(), []string{"openai", "auth", "status", "synthetic-secret"})
		require.EqualError(t, err, "Auth status takes no positional arguments. Use '/my CLI/openai' auth status --help.")
	})
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
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("synthetic-user:synthetic-password"))
	types := map[string]string{
		"": "none", "Bearer synthetic-api": "api_key", "Bearer synthetic-admin": "admin_api_key",
		"Bearer synthetic-header": "authorization_header", "Bearer synthetic-literal": "authorization_header", basic: "url_basic_auth",
	}
	for _, tc := range []struct {
		name                     string
		env                      map[string]string
		args                     []string
		userinfo                 bool
		api, admin, org, project string
	}{
		{name: "separate keys", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin"}, api: "Bearer synthetic-api", admin: "Bearer synthetic-admin"},
		{name: "explicit empty key retains header", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-header"}, args: []string{"--api-key="}, api: "Bearer synthetic-header", admin: "Bearer synthetic-header"},
		{name: "literal empty header", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin"}, args: []string{"-H", "Authorization:"}},
		{name: "empty context overrides headers", env: map[string]string{"OPENAI_ORG_ID": "", "OPENAI_PROJECT_ID": "", "OPENAI_CUSTOM_HEADERS": "OpenAI-Organization: org-header\nOpenAI-Project: proj-header"}},
		{name: "literal overrides", env: map[string]string{"OPENAI_API_KEY": "synthetic-api", "OPENAI_ADMIN_KEY": "synthetic-admin", "OPENAI_PROJECT_ID": "proj-env"}, args: []string{"-H", "Authorization: Bearer synthetic-literal", "-H", "OpenAI-Project: proj-literal"}, api: "Bearer synthetic-literal", admin: "Bearer synthetic-literal", project: "proj-literal"},
		{name: "API key and Admin URL Basic", env: map[string]string{"OPENAI_API_KEY": "synthetic-api"}, userinfo: true, api: "Bearer synthetic-api", admin: basic},
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
			baseURL := server.URL
			if tc.userinfo {
				baseURL = strings.Replace(baseURL, "://", "://synthetic-user:synthetic-password@", 1)
			}
			var out bytes.Buffer
			args := append([]string{"openai", "--base-url", baseURL, "auth", "status", "--format", "json"}, tc.args...)
			require.NoError(t, authStatusTestRoot(&out).Run(t.Context(), args))
			var status authStatusReport
			require.NoError(t, json.Unmarshal(out.Bytes(), &status))
			require.Empty(t, received, "auth status must never make an SDK request")
			for _, admin := range []bool{false, true} {
				root := authStatusTestRoot(io.Discard)
				root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, c *cli.Command) error {
					client := openai.NewClient(request.DefaultOptions(c, "test")...)
					headers, err := request.Headers(c)
					if err != nil {
						return err
					}
					var options []option.RequestOption
					for name, values := range headers {
						options = append(options, option.WithHeader(name, values[0]))
					}
					if admin {
						_, err = client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{}, options...)
					} else {
						_, err = client.Models.List(ctx, options...)
					}
					return err
				}})
				args := append([]string{"openai", "--base-url", baseURL, "probe"}, tc.args...)
				require.NoError(t, root.Run(t.Context(), args))
			}
			require.Len(t, received, 2)
			require.Equal(t, tc.api, received[0].Get("Authorization"))
			require.Equal(t, tc.admin, received[1].Get("Authorization"))
			for i, header := range received {
				require.Equal(t, tc.org, header.Get("OpenAI-Organization"))
				require.Equal(t, tc.project, header.Get("OpenAI-Project"))
				credential := []authStatusCredential{status.API, status.Admin}[i]
				require.Equal(t, header.Get("Authorization") != "", credential.Configured)
				require.Equal(t, types[header.Get("Authorization")], credential.Type)
				require.Equal(t, header.Get("OpenAI-Organization"), status.Organization.Value)
				require.Equal(t, header.Get("OpenAI-Project"), status.Project.Value)
			}
		})
	}
}
