package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	localcommands "github.com/openai/openai-cli/internal/commands"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func apiAuthGuidanceTestCommand(action cli.ActionFunc) *cli.Command {
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Metadata:       map[string]any{"help-invocation": "'/tmp/synthetic app/openai'"},
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "api-key", Sources: cli.EnvVars("OPENAI_API_KEY")},
			&requestflag.Flag[string]{Name: "admin-api-key", Sources: cli.EnvVars("OPENAI_ADMIN_KEY")},
			&requestflag.Flag[string]{Name: "organization", Sources: cli.EnvVars("OPENAI_ORG_ID")},
			&requestflag.Flag[string]{Name: "project", Sources: cli.EnvVars("OPENAI_PROJECT_ID")},
			&requestflag.Flag[string]{Name: "webhook-secret", Sources: cli.EnvVars("OPENAI_WEBHOOK_SECRET")},
			&cli.StringFlag{Name: "base-url"}, NewRequestHeaderFlag(),
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "format-error"},
			&cli.StringFlag{Name: "transform-error"}, &cli.BoolFlag{Name: "quiet"}, &cli.BoolFlag{Name: "debug"},
		}, Commands: []*cli.Command{
			{Name: "models", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list", Action: action}}},
			{Name: "admin:organization:projects", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list", Action: action}}},
			{Name: "synthetic", Commands: []*cli.Command{{Name: "create", Action: action,
				Flags: []cli.Flag{&requestflag.Flag[string]{Name: "project", BodyPath: "project"}}}}},
		},
	}
	requestconfig.ConfigureRootFlags(root)
	localcommands.ConfigureSubgroups(root)
	localcommands.ConfigureTasks(root)
	return root
}

func cleanAPIAuthGuidanceEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_WEBHOOK_SECRET", "OPENAI_CUSTOM_HEADERS", "OPENAI_BASE_URL"} {
		t.Setenv(name, "")
	}
}

func TestAPIAuthGuidanceConfigurationSources(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		args []string
		want []string
		omit []string
	}{
		{name: "missing", args: []string{"models", "list"}, want: []string{"No API key is configured", "Next: set OPENAI_API_KEY", apiAuthenticationKeyURL}},
		{name: "environment", env: map[string]string{"OPENAI_API_KEY": "synthetic-env"}, args: []string{"models", "list"}, want: []string{"An API key is configured through --api-key or OPENAI_API_KEY", "An explicit --api-key takes precedence"}},
		{name: "explicit", args: []string{"--api-key", "synthetic-flag", "models", "list"}, want: []string{"Credential source: --api-key", "takes precedence over OPENAI_API_KEY"}},
		{name: "explicit empty", env: map[string]string{"OPENAI_API_KEY": "synthetic-env"}, args: []string{"--api-key=", "models", "list"}, want: []string{"--api-key override is empty", "disables the OPENAI_API_KEY key", "remove the empty --api-key override"}, omit: []string{"An API key is configured"}},
		{name: "explicit header", env: map[string]string{"OPENAI_API_KEY": "synthetic-env"}, args: []string{"--header", "aUtHoRiZaTiOn: Bearer synthetic-header", "models", "list"}, want: []string{"--header controls authentication", "correct or remove this Authorization override"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "environment header", env: map[string]string{"OPENAI_CUSTOM_HEADERS": " Authorization : Bearer synthetic-header"}, args: []string{"models", "list"}, want: []string{"OPENAI_CUSTOM_HEADERS controls authentication"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "environment key replaces header", env: map[string]string{"OPENAI_API_KEY": "synthetic-env", "OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-header"}, args: []string{"models", "list"}, want: []string{"An API key is configured"}, omit: []string{"OPENAI_CUSTOM_HEADERS controls authentication"}},
		{name: "admin missing", env: map[string]string{"OPENAI_API_KEY": "synthetic-env"}, args: []string{"admin", "projects", "list"}, want: []string{"No admin API key is configured", "set OPENAI_ADMIN_KEY"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "admin colon", env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin"}, args: []string{"admin:organization:projects", "list"}, want: []string{"An admin API key is configured", "--admin-api-key", "OPENAI_ADMIN_KEY"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "projects shortcut", env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin"}, args: []string{"projects", "list"}, want: []string{"An admin API key is configured"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "admin key does not select admin route", env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin"}, args: []string{"models", "list"}, want: []string{"No API key is configured"}, omit: []string{"An admin API key"}},
		{name: "body project stays local", args: []string{"synthetic", "create", "--project", "synthetic-private-project"}, want: []string{"No API key is configured"}, omit: []string{"synthetic-private-project"}},
		{name: "custom provider", args: []string{"--base-url", "https://synthetic-private.invalid/v1", "models", "list"}, want: []string{"custom API address", "credential required by this API endpoint"}, omit: []string{apiAuthenticationKeyURL, "Next: set OPENAI_API_KEY"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleanAPIAuthGuidanceEnvironment(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			called := false
			root := apiAuthGuidanceTestCommand(func(_ context.Context, command *cli.Command) error {
				called = true
				apierr := &openai.Error{StatusCode: http.StatusUnauthorized, Message: "synthetic-private-body\x1b[31m", Param: "synthetic-private-param"}
				message := readableAPIAuthenticationMessage(command.Root(), withCommandError(command, apierr), apierr)
				for _, want := range test.want {
					require.Contains(t, message, want)
				}
				for _, omit := range append(test.omit, "synthetic-env", "synthetic-admin", "synthetic-flag", "synthetic-header", "synthetic-private", "\x1b") {
					require.NotContains(t, message, omit)
				}
				require.Contains(t, message, "Key setup: '/tmp/synthetic app/openai' help setup")
				return nil
			})
			require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, test.args...)))
			require.True(t, called)
		})
	}
}

func TestAPIAuthGuidanceKnownIPRestrictionAndCustomTargets(t *testing.T) {
	for _, base := range []string{"", "https://api.openai.com/v1/", "https://eu.api.openai.com/v1", "https://api.openai.com.evil.invalid/v1", "https://synthetic-private.invalid/v1"} {
		cleanAPIAuthGuidanceEnvironment(t)
		t.Setenv("OPENAI_BASE_URL", base)
		root := apiAuthGuidanceTestCommand(func(_ context.Context, command *cli.Command) error {
			apierr := &openai.Error{StatusCode: http.StatusUnauthorized, Code: "ip_not_authorized"}
			message := readableAPIAuthenticationMessage(command.Root(), withCommandError(command, apierr), apierr)
			require.Contains(t, message, "check your outbound IP in the allowlist")
			require.NotContains(t, message, apiAuthenticationKeyURL)
			require.NotContains(t, message, "synthetic-private")
			for _, irrelevant := range []string{"Key setup:", "Credential", "--project", "OPENAI_API_KEY", "OPENAI_ADMIN_KEY"} {
				require.NotContains(t, message, irrelevant)
			}
			if strings.Contains(base, ".invalid") {
				require.NotContains(t, message, apiAuthenticationIPAllowlistURL)
			} else {
				require.Contains(t, message, apiAuthenticationIPAllowlistURL)
			}
			return nil
		})
		require.NoError(t, root.Run(t.Context(), []string{"openai", "models", "list"}))
	}
}

type apiAuthGuidanceTransport func(*http.Request) (*http.Response, error)

func (f apiAuthGuidanceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestAPIAuthGuidanceMatchesSDKAuthorizationPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, apiKey, adminKey, customHeader, expected, source string
		args                                                   []string
		admin                                                  bool
	}{
		{name: "env header only", customHeader: "Authorization: Bearer synthetic-header", expected: "Bearer synthetic-header", source: "OPENAI_CUSTOM_HEADERS controls authentication"},
		{name: "env key replaces env header", apiKey: "synthetic-env", customHeader: "Authorization: Bearer synthetic-header", expected: "Bearer synthetic-env", source: "An API key is configured"},
		{name: "flag header wins", apiKey: "synthetic-env", customHeader: "Authorization: Bearer synthetic-header", args: []string{"--header", "Authorization: Bearer synthetic-explicit"}, expected: "Bearer synthetic-explicit", source: "--header controls authentication"},
		{name: "empty key keeps env header", apiKey: "synthetic-env", customHeader: "Authorization: Bearer synthetic-header", args: []string{"--api-key="}, expected: "Bearer synthetic-header", source: "OPENAI_CUSTOM_HEADERS controls authentication"},
		{name: "irrelevant admin key keeps env header", adminKey: "synthetic-admin", customHeader: "Authorization: Bearer synthetic-header", expected: "Bearer synthetic-header", source: "OPENAI_CUSTOM_HEADERS controls authentication"},
		{name: "admin key replaces env header", admin: true, adminKey: "synthetic-admin", customHeader: "Authorization: Bearer synthetic-header", expected: "Bearer synthetic-admin", source: "An admin API key is configured"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleanAPIAuthGuidanceEnvironment(t)
			t.Setenv("OPENAI_API_KEY", test.apiKey)
			t.Setenv("OPENAI_ADMIN_KEY", test.adminKey)
			t.Setenv("OPENAI_CUSTOM_HEADERS", test.customHeader)
			requests := 0
			root := apiAuthGuidanceTestCommand(func(ctx context.Context, command *cli.Command) error {
				options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
				require.NoError(t, err)
				clientOptions := GetDefaultRequestOptions(command)
				// The injected transport handles every request without network I/O.
				clientOptions = append(clientOptions, option.WithBaseURL("https://api.openai.com/v1/"))
				clientOptions = append(clientOptions, option.WithHTTPClient(&http.Client{Transport: apiAuthGuidanceTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, test.expected, request.Header.Get("Authorization"))
					return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"error":{"message":"synthetic-private-body","type":"invalid_request_error"}}`)), Request: request}, nil
				})}))
				client := openai.NewClient(clientOptions...)
				if test.admin {
					_, err = client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{}, options...)
				} else {
					_, err = client.Models.List(ctx, options...)
				}
				var apierr *openai.Error
				require.ErrorAs(t, err, &apierr)
				message := readableAPIAuthenticationMessage(command.Root(), withCommandError(command, err), apierr)
				require.Contains(t, message, test.source)
				require.NotContains(t, message, "synthetic-private-body")
				return nil
			})
			args := append([]string{"openai"}, test.args...)
			if test.admin {
				args = append(args, "admin", "projects", "list")
			} else {
				args = append(args, "models", "list")
			}
			require.NoError(t, root.Run(t.Context(), args))
			require.Equal(t, 1, requests)
		})
	}
}

func TestAPIAuthGuidancePresenterPreservesMachineAndCancellation(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		cleanAPIAuthGuidanceEnvironment(t)
		root := apiAuthGuidanceTestCommand(func(_ context.Context, command *cli.Command) error {
			apierr := &openai.Error{StatusCode: http.StatusUnauthorized}
			require.NoError(t, apierr.UnmarshalJSON([]byte(`{"message":"synthetic-private-body","code":"invalid_api_key","future":true}`)))
			apierr.StatusCode = http.StatusUnauthorized
			failure := withCommandError(command, apierr)
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(command.Root(), failure, &out))
			if format == "json" {
				require.JSONEq(t, apierr.RawJSON(), out.String())
				require.NotContains(t, out.String(), "Key setup:")
			} else {
				require.Contains(t, out.String(), "No API key is configured")
				require.Contains(t, out.String(), apiAuthenticationKeyURL)
				require.NotContains(t, out.String(), "synthetic-private-body")
				out.Reset()
				require.NoError(t, ShowCommandError(command.Root(), errors.Join(failure, context.Canceled), &out))
				require.Equal(t, "Request canceled.\n", out.String())
			}
			return nil
		})
		require.NoError(t, root.Run(t.Context(), []string{"openai", "--format-error", format, "models", "list"}))
	}
}

func TestAPIPermissionGuidanceScopeBoundaries(t *testing.T) {
	const prefix = "You have insufficient permissions for this operation."
	for _, test := range []struct {
		name, message string
		recognized    bool
		read, write   bool
	}{
		{"read with recovery prose", prefix + " Missing scopes: api.webhooks.read. Check that synthetic-private-body\x1b]52;c;synthetic-private-data\a", true, true, false},
		{"write final period", prefix + " Missing scopes: api.webhooks.write.", true, false, true},
		{"both and duplicate", prefix + " Missing scopes: api.webhooks.write, api.webhooks.read, api.webhooks.read.", true, true, true},
		{"unknown alongside read", prefix + " Missing scopes: synthetic-private-scope, api.webhooks.read.", true, true, false},
		{"unknown only", prefix + " Missing scopes: synthetic-private-scope.", true, false, false},
		{"scope suffix", prefix + " Missing scopes: api.webhooks.read.synthetic-private-scope.", true, false, false},
		{"scope prefix", prefix + " Missing scopes: synthetic-private-api.webhooks.read.", true, false, false},
		{"scope controls", prefix + " Missing scopes: api.webhooks.read\x1b[31m.", true, false, false},
		{"generic permissions", prefix, true, false, false},
		{"embedded prose", "synthetic-private-prefix " + prefix + " Missing scopes: api.webhooks.read.", false, false, false},
		{"missing terminator", prefix + " Missing scopes: api.webhooks.read", false, false, false},
		{"unrelated cause", "synthetic-private-body api.webhooks.read", false, false, false},
	} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(test.name+"/"+http.StatusText(status), func(t *testing.T) {
				cleanAPIAuthGuidanceEnvironment(t)
				t.Setenv("OPENAI_API_KEY", "sk-fake-permission-key")
				root := apiAuthGuidanceTestCommand(func(_ context.Context, command *cli.Command) error {
					apierr := &openai.Error{StatusCode: status, Message: test.message,
						Response: &http.Response{Header: http.Header{"X-Request-Id": {"req_permission_fixture"}}}}
					var out bytes.Buffer
					require.NoError(t, ShowCommandError(command.Root(), withCommandError(command, apierr), &out))
					message := out.String()
					require.Equal(t, test.recognized, strings.Contains(message, "The server denied this operation because required permissions are missing."))
					for scope, present := range map[string]bool{"api.webhooks.read": test.read, "api.webhooks.write": test.write} {
						if present {
							require.Equal(t, 1, strings.Count(message, "Missing permission: "+scope+".\n"))
						} else {
							require.NotContains(t, message, scope)
						}
					}
					if test.recognized {
						require.Contains(t, message, "Next: check this key's access and your organization and project roles.")
						require.NotContains(t, message, "did not accept authentication")
						require.NotContains(t, message, "Next: set OPENAI_API_KEY")
					}
					if test.read || test.write {
						for _, hint := range []string{"Read only may omit webhook permissions.", "Restricted may not offer a Webhooks permission.",
							"All grants broader permissions within your organization and project roles. Select it only deliberately."} {
							require.Contains(t, message, hint)
						}
					} else {
						require.NotContains(t, message, "All grants")
					}
					require.Contains(t, message, "Request ID: req_permission_fixture")
					for _, secret := range []string{"sk-fake-permission-key", "synthetic-private", "\x1b", "\a"} {
						require.NotContains(t, message, secret)
					}
					return nil
				})
				require.NoError(t, root.Run(t.Context(), []string{"openai", "models", "list"}))
			})
		}
	}
}

func TestAPIPermissionGuidanceOriginsAndOverrides(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
		link bool
	}{
		{"OpenAI explicit origin", []string{"--base-url=https://api.openai.com/v1/", "models", "list"}, "check this key's access", true},
		{"custom origin", []string{"--base-url=https://synthetic-private.invalid/v1", "models", "list"}, "your role for this API endpoint", false},
		{"lookalike origin", []string{"--base-url=https://api.openai.com.synthetic-private.invalid/v1", "models", "list"}, "your role for this API endpoint", false},
		{"Authorization override", []string{"--header", "Authorization: Bearer sk-fake-permission-override", "models", "list"}, "check the overridden credential's permissions", false},
		{"admin route", []string{"admin", "projects", "list"}, "check this admin key's permissions and your role", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleanAPIAuthGuidanceEnvironment(t)
			root := apiAuthGuidanceTestCommand(func(_ context.Context, command *cli.Command) error {
				apierr := &openai.Error{StatusCode: http.StatusUnauthorized,
					Message: "You have insufficient permissions for this operation. Missing scopes: api.webhooks.read."}
				var out bytes.Buffer
				require.NoError(t, ShowCommandError(command.Root(), withCommandError(command, apierr), &out))
				require.Contains(t, out.String(), test.want)
				require.Equal(t, test.link, strings.Contains(out.String(), apiAuthenticationKeyURL))
				require.Equal(t, test.link, strings.Contains(out.String(), "All grants broader permissions"))
				require.NotContains(t, out.String(), "synthetic-private")
				require.NotContains(t, out.String(), "sk-fake-permission-override")
				return nil
			})
			require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, test.args...)))
		})
	}
}

func TestAPIPermissionGuidanceCancellationAndStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest} {
		cleanAPIAuthGuidanceEnvironment(t)
		root := apiAuthGuidanceTestCommand(func(_ context.Context, command *cli.Command) error {
			apierr := &openai.Error{StatusCode: status,
				Message: "You have insufficient permissions for this operation. Missing scopes: api.webhooks.read."}
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(command.Root(), withCommandError(command, errors.Join(apierr, context.Canceled)), &out))
			require.Equal(t, "Request canceled.\n", out.String())
			if status == http.StatusBadRequest {
				out.Reset()
				require.NoError(t, ShowCommandError(command.Root(), withCommandError(command, apierr), &out))
				require.NotContains(t, out.String(), "Missing permission:")
			}
			return nil
		})
		require.NoError(t, root.Run(t.Context(), []string{"openai", "models", "list"}))
	}
}
