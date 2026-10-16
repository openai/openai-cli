package custom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAdminSetupOriginDisplayPreservesDestination(t *testing.T) {
	for _, test := range []struct{ endpoint, display string }{
		{"https://api.openai.com/v1", "https://api.openai.com"},
		{"https://gateway.example.test:8443/v1", "https://gateway.example.test:8443"},
		{"http://[::1]:8080/v1", "http://[::1]:8080"},
		{"https://\u0430pi.example.test/v1", `https://\u0430pi.example.test`},
		{"https://caf\u00e9.example.test/v1", `https://caf\u00e9.example.test`},
		{"https://prefix\u202e.example.test/v1", `https://prefix\u202e.example.test`},
		{"https://prefix\u009b.example.test/v1", `https://prefix\u009b.example.test`},
	} {
		t.Run(test.display, func(t *testing.T) {
			endpoint, err := url.Parse(test.endpoint)
			require.NoError(t, err)
			requests := 0
			root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
				options, display, err := adminSetupRequestOptions(command)
				if err != nil {
					return err
				}
				require.Equal(t, test.display, display)
				return verifyAdminSetup(ctx, options, []byte("synthetic-entered-admin-key"))
			})
			root.Metadata = map[string]any{mtlsHTTPClientMetadata: &http.Client{
				Transport: adminSetupOriginTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, endpoint.Scheme, request.URL.Scheme)
					require.Equal(t, endpoint.Host, request.URL.Host)
					require.Equal(t, "/v1/organization/projects?limit=1", request.URL.RequestURI())
					require.Equal(t, "Bearer synthetic-entered-admin-key", request.Header.Get("Authorization"))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[],"has_more":false}`)), Request: request}, nil
				}),
			}}
			require.NoError(t, root.Run(t.Context(), []string{root.Name, "--base-url", test.endpoint, "admin:organization:projects", "list"}))
			require.Equal(t, 1, requests)
		})
	}
}

func TestAdminSetupProfileEndpointOptions(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, wantURL, wantDisplay string
		absentEndpoint                       bool
		env                                  map[string]string
		args                                 []string
	}{
		{name: "saved destination", endpoint: "https://gateway.example.test/custom/v1", wantURL: "https://gateway.example.test/custom/v1/organization/projects?limit=1", wantDisplay: "https://gateway.example.test"},
		{name: "empty saved endpoint", wantURL: "https://api.openai.com/v1/organization/projects?limit=1", wantDisplay: "https://api.openai.com"},
		{name: "absent saved endpoint", absentEndpoint: true, wantURL: "https://api.openai.com/v1/organization/projects?limit=1", wantDisplay: "https://api.openai.com"},
		{name: "empty flag retains profile", endpoint: "https://gateway.example.test/v1", args: []string{"--base-url="}, wantURL: "https://gateway.example.test/v1/organization/projects?limit=1", wantDisplay: "https://gateway.example.test"},
		{name: "environment overrides profile", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_BASE_URL": "https://environment.example.test/v1/"}, wantURL: "https://environment.example.test/v1/organization/projects?limit=1", wantDisplay: "https://environment.example.test"},
		{name: "flag overrides environment", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_BASE_URL": "https://environment.example.test/v1/"}, args: []string{"--base-url=https://flag.example.test/v1/"}, wantURL: "https://flag.example.test/v1/organization/projects?limit=1", wantDisplay: "https://flag.example.test"},
		{name: "environment selector", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_PROFILE": "work"}, wantURL: "https://gateway.example.test/v1/organization/projects?limit=1", wantDisplay: "https://gateway.example.test"},
		{name: "disabled default", endpoint: "https://gateway.example.test/v1", args: []string{"--profile="}, wantURL: "https://api.openai.com/v1/organization/projects?limit=1", wantDisplay: "https://api.openai.com"},
		{name: "escaped saved origin", endpoint: "https://prefix\u202e.example.test/v1", wantURL: "https://prefix\u202e.example.test/v1/organization/projects?limit=1", wantDisplay: `https://prefix\u202e.example.test`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected, err := url.Parse(tc.wantURL)
			require.NoError(t, err)
			requests := 0
			root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
				options, display, err := adminSetupRequestOptions(command)
				if err != nil {
					return err
				}
				require.Equal(t, tc.wantDisplay, display)
				return verifyAdminSetup(ctx, options, []byte("synthetic-entered-key"))
			})
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			t.Setenv("OPENAI_ADMIN_KEY", "synthetic-environment-key")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer synthetic-environment-header\nOpenAI-Project: proj_inherited\nX-Api-Key: synthetic-gateway-key")
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			path, err := profileStorePath()
			require.NoError(t, err)
			_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				values := profileValues{}
				if !tc.absentEndpoint {
					values.BaseURL = &tc.endpoint
				}
				registry.Profiles["work"] = values
				name := "work"
				registry.DefaultProfile = &name
				return nil
			})
			require.NoError(t, err)
			configureProfileContext(root)
			root.Metadata = map[string]any{mtlsHTTPClientMetadata: &http.Client{
				Transport: adminSetupOriginTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, expected.Scheme, request.URL.Scheme)
					require.Equal(t, expected.Host, request.URL.Host)
					require.Equal(t, expected.RequestURI(), request.URL.RequestURI())
					require.Equal(t, "Bearer synthetic-entered-key", request.Header.Get("Authorization"))
					require.Equal(t, "proj_literal", request.Header.Get("OpenAI-Project"))
					require.Equal(t, "synthetic-gateway-key", request.Header.Get("X-Api-Key"))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[],"has_more":false}`)), Request: request}, nil
				}),
			}}
			args := []string{root.Name, "--admin-api-key=synthetic-flag-key", "-H", "Authorization: Bearer synthetic-literal-key", "-H", "OpenAI-Project: proj_literal"}
			args = append(args, tc.args...)
			require.NoError(t, root.Run(t.Context(), append(args, "admin:organization:projects", "list")))
			require.Equal(t, 1, requests)
		})
	}
}

func TestAdminSetupProfileEndpointRejectsUnsafeOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, profileURL, environmentURL string
		useEnvironment                   bool
	}{
		{name: "profile HTTP", profileURL: "http://gateway.example.test/v1"},
		{name: "empty environment", profileURL: "https://gateway.example.test/v1", useEnvironment: true},
		{name: "environment HTTP", environmentURL: "http://gateway.example.test/v1", useEnvironment: true},
		{name: "embedded credential", environmentURL: "https://synthetic-user:synthetic-secret@gateway.example.test/v1", useEnvironment: true},
		{name: "query", environmentURL: "https://gateway.example.test/v1?key=synthetic-secret", useEnvironment: true},
		{name: "fragment", environmentURL: "https://gateway.example.test/v1#synthetic-secret", useEnvironment: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := adminCredentialsTestCommand(t, func(_ context.Context, command *cli.Command) error {
				options, display, err := adminSetupRequestOptions(command)
				require.Nil(t, options)
				require.Empty(t, display)
				return err
			})
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			if tc.useEnvironment {
				t.Setenv("OPENAI_BASE_URL", tc.environmentURL)
			}
			path, err := profileStorePath()
			require.NoError(t, err)
			_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				registry.Profiles["work"] = profileValues{BaseURL: &tc.profileURL}
				name := "work"
				registry.DefaultProfile = &name
				return nil
			})
			require.NoError(t, err)
			configureProfileContext(root)
			root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
			err = root.Run(t.Context(), []string{root.Name, "admin:organization:projects", "list"})
			var setup *adminSetupError
			require.ErrorAs(t, err, &setup)
			require.Equal(t, 3, setup.ExitCode())
			require.NotContains(t, err.Error(), "synthetic-secret")
			require.NotContains(t, err.Error(), "gateway.example.test")
		})
	}
}

func TestAdminSetupProfileResolutionFailures(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "missing"
		if malformed {
			name = "malformed"
		}
		t.Run(name, func(t *testing.T) {
			root := adminCredentialsTestCommand(t, func(_ context.Context, command *cli.Command) error {
				options, display, err := adminSetupRequestOptions(command)
				require.Nil(t, options)
				require.Empty(t, display)
				return err
			})
			t.Setenv("OPENAI_PROFILE", "missing")
			if malformed {
				path, err := profileStorePath()
				require.NoError(t, err)
				writeProfileRegistryTestFile(t, path, `{"synthetic-sensitive-field":"synthetic-sensitive-value"}`)
			}
			configureProfileContext(root)
			err := root.Run(t.Context(), []string{root.Name, "--base-url=https://gateway.example.test/v1/", "admin:organization:projects", "list"})
			var local *localUtilityError
			require.ErrorAs(t, err, &local)
			require.NotContains(t, err.Error(), "synthetic-sensitive")
		})
	}
}

func TestAdminSetupProfileEndpointLoopback(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			type requestDetails struct{ uri, authorization, organization, project string }
			observed := make(chan requestDetails, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				select {
				case observed <- requestDetails{request.URL.RequestURI(), request.Header.Get("Authorization"), request.Header.Get("OpenAI-Organization"), request.Header.Get("OpenAI-Project")}:
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/redirected" {
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
					return
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
				} else {
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic-entered-key"}}`)
				}
			}))
			t.Cleanup(server.Close)
			root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
				options, display, err := adminSetupRequestOptions(command)
				if err != nil {
					return err
				}
				require.Equal(t, server.URL, display)
				return verifyAdminSetup(ctx, options, []byte("synthetic-entered-key"))
			})
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			t.Setenv("OPENAI_ADMIN_KEY", "synthetic-environment-key")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer synthetic-environment-header\nOpenAI-Organization: org_inherited\nOpenAI-Project: proj_inherited")
			path, err := profileStorePath()
			require.NoError(t, err)
			_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				endpoint, organization, project, name := server.URL+"/saved/v1", "org_profile", "proj_profile", "work"
				registry.Profiles[name] = profileValues{Organization: &organization, Project: &project, BaseURL: &endpoint}
				registry.DefaultProfile = &name
				return nil
			})
			require.NoError(t, err)
			configureProfileContext(root)
			client := server.Client()
			redirects := 0
			client.CheckRedirect = func(*http.Request, []*http.Request) error { redirects++; return nil }
			root.Metadata = map[string]any{mtlsHTTPClientMetadata: client}
			root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
			err = root.Run(t.Context(), []string{root.Name, "admin:organization:projects", "list"})
			if status == http.StatusOK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "synthetic-entered-key")
				if status == http.StatusFound {
					require.ErrorContains(t, err, "stopped at a redirect")
				} else {
					require.ErrorContains(t, err, "denied admin access (403)")
				}
			}
			require.EqualValues(t, 1, requests.Load())
			select {
			case request := <-observed:
				require.Equal(t, requestDetails{"/saved/v1/organization/projects?limit=1", "Bearer synthetic-entered-key", "org_profile", "proj_profile"}, request)
			default:
				t.Fatal("verification did not reach the profile endpoint")
			}
			require.Zero(t, redirects)
			require.NoError(t, client.CheckRedirect(nil, nil))
			require.Equal(t, 1, redirects)
		})
	}
}

type adminSetupOriginTransport func(*http.Request) (*http.Response, error)

func (transport adminSetupOriginTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
