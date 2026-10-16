package custom

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func profileContextTestRoot(t *testing.T) *cli.Command {
	t.Helper()
	root := authStatusTestRoot(io.Discard)
	registerProjectLinkCommands(root)
	configureProfileContext(root)
	return root
}

func TestProfileContextWrappedFlagMatchesRequest(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, map[string]string{"OPENAI_CUSTOM_HEADERS": "OpenAI-Project: proj_header"})
	root := profileContextTestRoot(t)
	root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, command *cli.Command) error {
		report, err := resolveAuthStatus(root, os.LookupEnv)
		require.NoError(t, err)
		require.Equal(t, authStatusValue{Value: "proj_flag", Source: "flag:--project"}, report.Project)
		var got string
		options := append(GetDefaultRequestOptions(command), option.WithHTTPClient(profileContextTransport(func(request *http.Request) {
			got = request.Header.Get("OpenAI-Project")
		})))
		client := openai.NewClient(options...)
		_, err = client.Models.List(ctx)
		require.NoError(t, err)
		require.Equal(t, report.Project.Value, got)
		return nil
	}})
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project", "proj_flag", "probe"}))
}

type profileContextTransport func(*http.Request)

func (capture profileContextTransport) Do(request *http.Request) (*http.Response, error) {
	capture(request)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"data":[],"object":"list"}`)), Request: request}, nil
}

func TestProfileContextSnapshotAndCommandReuse(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, nil)
	t.Setenv("OPENAI_PROFILE", "work")
	path, err := profileStorePath()
	require.NoError(t, err)
	first, second := "proj_first", "proj_second"
	_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["work"] = profileValues{Project: &first}
		return nil
	})
	require.NoError(t, err)
	root := profileContextTestRoot(t)
	var got []string
	root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, command *cli.Command) error {
		for range 2 {
			options := append(GetDefaultRequestOptions(command), option.WithHTTPClient(profileContextTransport(func(request *http.Request) {
				got = append(got, request.Header.Get("OpenAI-Project"))
			})))
			client := openai.NewClient(options...)
			_, err := client.Models.List(ctx)
			if err != nil {
				return err
			}
			_, err = updateProfiles(ctx, path, func(registry *profileRegistry) error {
				registry.Profiles["work"] = profileValues{Project: &second}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	}})
	require.NoError(t, root.Run(t.Context(), []string{"openai", "probe"}))
	require.Equal(t, []string{first, first}, got)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project=", "probe"}))
	require.Equal(t, []string{first, first, "", ""}, got)
}

func TestProfileContextEnvironmentSnapshot(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, nil)
	root := profileContextTestRoot(t)
	calls := make(map[string]int)
	root.Command("auth").Command("status").Action = func(ctx context.Context, command *cli.Command) error {
		_, err := resolveAuthStatus(root, func(name string) (string, bool) {
			calls[name]++
			return "", false
		})
		return err
	}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "auth", "status"}))
	require.Len(t, calls, 7)
	for name, count := range calls {
		require.Equal(t, 1, count, name)
	}
}
