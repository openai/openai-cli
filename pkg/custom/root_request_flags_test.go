package custom

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestRootRequestFlagsPreserveOwnershipAndMetadata(t *testing.T) {
	project := &requestflag.Flag[string]{Name: "project", Usage: "Project context", Aliases: []string{"p"}}
	apiKey := &requestflag.Flag[string]{Name: "api-key", Default: "synthetic-secret"}
	endpoint := &requestflag.Flag[string]{Name: "project", BodyPath: "project"}
	unrelated := &requestflag.Flag[string]{Name: "model"}
	format := &cli.StringFlag{Name: "format"}
	root := &cli.Command{
		Flags:    []cli.Flag{project, apiKey, unrelated, format},
		Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{endpoint}}},
	}
	configureRootRequestFlags(root)
	wrapped := root.Flags[0].(*rootRequestFlag)
	require.Same(t, project, wrapped.RequestFlag())
	require.Equal(t, []string{"project", "p"}, wrapped.Names())
	require.Equal(t, "Project context", wrapped.GetUsage())
	require.False(t, wrapped.IsLocal())
	require.False(t, root.Flags[1].(cli.DocGenerationFlag).IsDefaultVisible())
	require.True(t, endpoint.IsLocal())
	require.Same(t, endpoint, root.Commands[0].Flags[0])
	require.Same(t, unrelated, root.Flags[2])
	require.Same(t, format, root.Flags[3])
	configureRootRequestFlags(root)
	require.Same(t, wrapped, root.Flags[0])
}

func TestRootRequestFlagsExplicitAssignmentProvenance(t *testing.T) {
	for _, name := range []string{"api-key", "admin-api-key", "organization", "project", "webhook-secret"} {
		t.Run(name, func(t *testing.T) {
			flag := "--" + name
			for _, tc := range []struct {
				name        string
				environment string
				args        []string
				want        string
				explicit    bool
			}{
				{name: "environment", environment: "synthetic-value", args: []string{"models", "list"}, want: "synthetic-value"},
				{name: "empty environment", args: []string{"models", "list"}},
				{name: "before command", environment: "synthetic-env", args: []string{flag, "synthetic-flag", "models", "list"}, want: "synthetic-flag", explicit: true},
				{name: "between commands", environment: "synthetic-env", args: []string{"models", flag, "synthetic-flag", "list"}, want: "synthetic-flag", explicit: true},
				{name: "after command", environment: "synthetic-env", args: []string{"models", "list", flag, "synthetic-flag"}, want: "synthetic-flag", explicit: true},
				{name: "alias", environment: "synthetic-env", args: []string{"models", "list", "-p", "synthetic-flag"}, want: "synthetic-flag", explicit: true},
				{name: "equal values", environment: "synthetic-value", args: []string{"models", "list", flag + "=synthetic-value"}, want: "synthetic-value", explicit: true},
				{name: "explicit empty", environment: "synthetic-env", args: []string{"models", flag + "=", "list"}, explicit: true},
				{name: "repeated", environment: "synthetic-env", args: []string{flag + "=first", "models", flag + "=second", "list", flag + "=last"}, want: "last", explicit: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv("OPENAI_TEST_ROOT_REQUEST_PROVENANCE", tc.environment)
					called := false
					root := &cli.Command{
						Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
						Flags: []cli.Flag{&requestflag.Flag[string]{
							Name: name, Aliases: []string{"p"}, Sources: cli.EnvVars("OPENAI_TEST_ROOT_REQUEST_PROVENANCE"),
						}},
						Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
							Name: "list",
							Action: func(_ context.Context, command *cli.Command) error {
								called = true
								wrapped := command.Root().Flags[0].(*rootRequestFlag)
								require.Equal(t, tc.explicit, wrapped.explicitlySet)
								require.True(t, command.Root().IsSet(name))
								require.Equal(t, tc.want, command.Root().String(name))
								require.Equal(t, tc.want, command.String(name))
								return nil
							},
						}}}},
					}
					configureRootRequestFlags(root)
					require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, tc.args...)))
					require.True(t, called)
				})
			}
		})
	}
}

func TestRootRequestFlagPreParseResetsInvocationState(t *testing.T) {
	wrapped := &rootRequestFlag{Flag: &requestflag.Flag[string]{Name: "project", Default: "default-project"}}
	require.NoError(t, wrapped.PreParse())
	require.False(t, wrapped.explicitlySet)
	require.False(t, wrapped.IsSet())
	require.NoError(t, wrapped.Set("project", "explicit-project"))
	require.True(t, wrapped.explicitlySet)
	require.Equal(t, 1, wrapped.Count())

	require.NoError(t, wrapped.PreParse())
	require.False(t, wrapped.explicitlySet)
	require.False(t, wrapped.IsSet())
	require.Zero(t, wrapped.Count())
	require.Equal(t, "default-project", wrapped.Get())
}

func TestRootRequestFlagsResetProvenanceAcrossRuns(t *testing.T) {
	for _, name := range []string{"api-key", "admin-api-key", "organization", "project", "webhook-secret"} {
		t.Run(name, func(t *testing.T) {
			const variable = "OPENAI_TEST_ROOT_REQUEST_REUSE"
			t.Setenv(variable, "")
			rejected := errors.New("rejected synthetic value")
			definition := &requestflag.Flag[string]{Name: name, Aliases: []string{"p"}, Sources: cli.EnvVars(variable),
				Validator: func(value string) error {
					if value == "invalid" {
						return rejected
					}
					return nil
				}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{definition}, Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{Name: "list"}}}},
			}
			configureRootRequestFlags(root)
			wrapped := root.Flags[0].(*rootRequestFlag)
			for _, tc := range []struct {
				name, environment, want string
				args                    []string
				absent, explicit, set   bool
				count                   int
				err                     error
			}{
				{name: "initial environment", environment: "synthetic-first", want: "synthetic-first", set: true, count: 1},
				{name: "changed environment", environment: "synthetic-second", want: "synthetic-second", set: true, count: 1},
				{name: "explicit repeated alias", environment: "synthetic-second", args: []string{"--" + name + "=synthetic-first", "-p", "synthetic-last"}, want: "synthetic-last", explicit: true, set: true, count: 2},
				{name: "environment after explicit", environment: "synthetic-third", want: "synthetic-third", set: true, count: 1},
				{name: "explicit empty", environment: "synthetic-third", args: []string{"--" + name + "="}, explicit: true, set: true, count: 1},
				{name: "present empty environment", set: true},
				{name: "removed environment", absent: true},
				{name: "validation failure", environment: "synthetic-third", args: []string{"--" + name + "=invalid"}, err: rejected},
				{name: "recovery after failure", environment: "synthetic-recovered", want: "synthetic-recovered", set: true, count: 1},
			} {
				t.Setenv(variable, tc.environment)
				if tc.absent {
					require.NoError(t, os.Unsetenv(variable))
				}
				called := false
				root.Command("models").Command("list").Action = func(_ context.Context, command *cli.Command) error {
					called = true
					require.Same(t, definition, wrapped.RequestFlag(), tc.name)
					require.Equal(t, tc.explicit, wrapped.ExplicitRequestFlag(), tc.name)
					require.Equal(t, tc.set, root.IsSet(name), tc.name)
					require.Equal(t, tc.set, command.IsSet(name), tc.name)
					require.Equal(t, tc.want, root.String(name), tc.name)
					require.Equal(t, tc.want, command.String(name), tc.name)
					require.Equal(t, tc.count, wrapped.Count(), tc.name)
					return nil
				}
				args := append([]string{"openai", "models", "list"}, tc.args...)
				err := root.Run(t.Context(), args)
				if tc.err != nil {
					require.ErrorContains(t, err, tc.err.Error(), tc.name)
					require.False(t, called, tc.name)
				} else {
					require.NoError(t, err, tc.name)
					require.True(t, called, tc.name)
				}
			}
		})
	}
}

func TestRootRequestFlagPreParsePreservesCurrentDefinition(t *testing.T) {
	var validated []string
	definition := &requestflag.Flag[string]{
		Name: "project", Category: "Synthetic category", DefaultText: "Synthetic default",
		HideDefault: true, Usage: "Synthetic usage", Sources: cli.EnvVars("OPENAI_TEST_ROOT_METADATA"),
		Required: true, Hidden: true, Default: "synthetic-default", Aliases: []string{"p"},
		Validator: func(value string) error { validated = append(validated, value); return nil },
		QueryPath: "synthetic_query", HeaderPath: "X-Synthetic", BodyPath: "synthetic_body", BodyRoot: true,
		PathParam: "synthetic_path", Const: true, FileInput: true, DataAliases: []string{"synthetic_alias"},
	}
	wrapped := &rootRequestFlag{Flag: definition}
	require.NoError(t, wrapped.PreParse())
	require.NoError(t, wrapped.Set("project", "synthetic-explicit"))
	definition.Usage, definition.Default = "Updated synthetic usage", "synthetic-updated"
	before := reflect.ValueOf(*definition)
	require.NoError(t, wrapped.PreParse())
	require.Same(t, definition, wrapped.RequestFlag())
	after := reflect.ValueOf(*definition)
	for index := 0; index < before.NumField(); index++ {
		field := before.Type().Field(index)
		if !field.IsExported() {
			continue
		}
		if field.Type.Kind() == reflect.Func {
			require.Equal(t, before.Field(index).Pointer(), after.Field(index).Pointer(), field.Name)
		} else {
			require.Equal(t, before.Field(index).Interface(), after.Field(index).Interface(), field.Name)
		}
	}
	require.Equal(t, []string{"synthetic-default", "synthetic-explicit", "synthetic-updated"}, validated)
	require.Equal(t, "synthetic-updated", wrapped.Get())
	require.True(t, wrapped.IsSet(), "Const remains set without an invocation assignment")
	require.Zero(t, wrapped.Count())
	require.False(t, wrapped.ExplicitRequestFlag())
}

func TestRootRequestFlagsReuseMatchesStatusAndSDKRequests(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, nil)
	t.Setenv("OPENAI_PROFILE", "work")
	path, err := profileStorePath()
	require.NoError(t, err)
	organization, project := "org_profile", "proj_profile"
	_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["work"] = profileValues{Organization: &organization, Project: &project}
		return nil
	})
	require.NoError(t, err)
	root := profileContextTestRoot(t)
	root.Commands = append(root.Commands, &cli.Command{Name: "probe"})
	for _, tc := range []struct {
		name, api, admin, organization, project, source string
		args                                            []string
		remove                                          bool
	}{
		{name: "environment", api: "synthetic-api-first", admin: "synthetic-admin-first", organization: "org_first", project: "proj_first", source: "environment"},
		{name: "explicit", api: "synthetic-api-flag", admin: "synthetic-admin-flag", organization: "org_flag", project: "proj_flag", source: "flag",
			args: []string{"--api-key=synthetic-api-flag", "--admin-api-key=synthetic-admin-flag", "--organization=org_flag", "--project=proj_flag"}},
		{name: "changed environment", api: "synthetic-api-second", admin: "synthetic-admin-second", organization: "org_second", project: "proj_second", source: "environment"},
		{name: "explicit empty", source: "flag", args: []string{"--api-key=", "--admin-api-key=", "--organization=", "--project="}},
		{name: "environment restored", api: "synthetic-api-second", admin: "synthetic-admin-second", organization: "org_second", project: "proj_second", source: "environment"},
		{name: "removed environment", organization: "org_profile", project: "proj_profile", source: "profile", remove: true},
	} {
		if tc.source == "environment" {
			for name, value := range map[string]string{"OPENAI_API_KEY": tc.api, "OPENAI_ADMIN_KEY": tc.admin, "OPENAI_ORG_ID": tc.organization, "OPENAI_PROJECT_ID": tc.project} {
				t.Setenv(name, value)
			}
		}
		if tc.remove {
			for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID"} {
				require.NoError(t, os.Unsetenv(name))
			}
		}
		called := false
		root.Command("probe").Action = func(ctx context.Context, command *cli.Command) error {
			called = true
			report, err := resolveAuthStatus(root, os.LookupEnv)
			require.NoError(t, err, tc.name)
			require.Equal(t, tc.api != "", report.API.Configured, tc.name)
			require.Equal(t, tc.admin != "", report.Admin.Configured, tc.name)
			apiSource, adminSource := "unset", "unset"
			if tc.source == "environment" {
				apiSource, adminSource = "environment:OPENAI_API_KEY", "environment:OPENAI_ADMIN_KEY"
			} else if tc.source == "flag" {
				apiSource, adminSource = "flag:--api-key", "flag:--admin-api-key"
			}
			require.Equal(t, apiSource, report.API.Source, tc.name)
			require.Equal(t, adminSource, report.Admin.Source, tc.name)
			require.Equal(t, tc.organization, report.Organization.Value, tc.name)
			require.Equal(t, tc.project, report.Project.Value, tc.name)
			wantSource := tc.source + ":"
			for _, source := range []string{report.Organization.Source, report.Project.Source} {
				require.Contains(t, source, wantSource, tc.name)
			}
			var requests []http.Header
			options := append(GetDefaultRequestOptions(command), option.WithHTTPClient(profileContextTransport(func(request *http.Request) {
				requests = append(requests, request.Header.Clone())
			})))
			client := openai.NewClient(options...)
			_, err = client.Models.List(ctx)
			require.NoError(t, err, tc.name)
			_, err = client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{})
			require.NoError(t, err, tc.name)
			require.Len(t, requests, 2, tc.name)
			for index, key := range []string{tc.api, tc.admin} {
				authorization := ""
				if key != "" {
					authorization = "Bearer " + key
				}
				require.Equal(t, authorization, requests[index].Get("Authorization"), tc.name)
				require.Equal(t, tc.organization, requests[index].Get("OpenAI-Organization"), tc.name)
				require.Equal(t, tc.project, requests[index].Get("OpenAI-Project"), tc.name)
			}
			return nil
		}
		args := append([]string{"openai", "probe"}, tc.args...)
		require.NoError(t, root.Run(t.Context(), args), tc.name)
		require.True(t, called, tc.name)
	}
}

func TestRootRequestFlagsPreserveMTLSPlacement(t *testing.T) {
	t.Setenv(mtlsClientCertFileEnv, "/missing/synthetic-env-certificate")
	t.Setenv(mtlsClientKeyFileEnv, "/missing/synthetic-env-key")
	pki := newMTLSTestPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.rootPEM))
	requests := make(chan http.Header, 3)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("request requires a verified client certificate")
		}
		requests <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	configureTestServerTrust(t, server)
	certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)
	flags := []string{"--project=proj-mtls", "--api-key=synthetic-mtls-key", "--base-url=" + server.URL,
		"--mtls-client-cert-file=" + certFile, "--mtls-client-key-file=" + keyFile}
	for _, position := range []int{0, 1, 2} {
		root := &cli.Command{
			Name: "openai",
			Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "project"},
				&requestflag.Flag[string]{Name: "api-key"},
				&cli.StringFlag{Name: "base-url"},
			},
			Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
				Name: "list",
				Action: func(ctx context.Context, command *cli.Command) error {
					client := openai.NewClient(GetDefaultRequestOptions(command)...)
					_, err := client.Models.List(ctx)
					return err
				},
			}}}},
		}
		configureRootRequestFlags(root)
		ConfigureCommand(root)
		route := []string{"models", "list"}
		args := append([]string{"openai"}, route[:position]...)
		args = append(args, flags...)
		args = append(args, route[position:]...)
		require.NoError(t, root.Run(t.Context(), args), "position %d", position)
		select {
		case headers := <-requests:
			require.Equal(t, "proj-mtls", headers.Get("OpenAI-Project"))
			require.Equal(t, "Bearer synthetic-mtls-key", headers.Get("Authorization"))
		default:
			t.Fatal("mTLS request was not received")
		}
	}
}

func TestRootRequestFlagsRetainSourcesAliasesAndValidation(t *testing.T) {
	t.Setenv("OPENAI_TEST_PROJECT", "env-project")
	rejected := errors.New("rejected project")
	for _, tc := range []struct {
		name string
		args []string
		want string
		err  error
	}{
		{name: "environment", args: []string{"models", "list"}, want: "env-project"},
		{name: "alias", args: []string{"models", "list", "-p", "explicit"}, want: "explicit"},
		{name: "empty", args: []string{"models", "--project=", "list"}, want: ""},
		{name: "duplicate", args: []string{"--project=first", "models", "--project=second", "list", "-p", "last"}, want: "last"},
		{name: "validation", args: []string{"models", "list", "--project=invalid"}, err: rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			root := &cli.Command{
				Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&requestflag.Flag[string]{
					Name: "project", Aliases: []string{"p"}, Sources: cli.EnvVars("OPENAI_TEST_PROJECT"),
					Validator: func(value string) error {
						if value == "invalid" {
							return rejected
						}
						return nil
					},
				}},
				Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
					Name: "list",
					Action: func(_ context.Context, command *cli.Command) error {
						called = true
						require.True(t, command.Root().IsSet("project"))
						require.Equal(t, tc.want, command.String("project"))
						require.Equal(t, tc.want, command.Root().String("project"))
						return nil
					},
				}}}},
			}
			configureRootRequestFlags(root)
			err := root.Run(t.Context(), append([]string{"openai"}, tc.args...))
			if tc.err != nil {
				require.ErrorContains(t, err, tc.err.Error())
				require.False(t, called)
			} else {
				require.NoError(t, err)
				require.True(t, called)
			}
		})
	}
}
