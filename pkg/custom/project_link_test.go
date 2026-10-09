package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func projectLinkTestCommand(out io.Writer) *cli.Command {
	root := &cli.Command{Name: "openai", Writer: out, ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&requestflag.Flag[string]{Name: "project", Sources: cli.EnvVars("OPENAI_PROJECT_ID")},
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
	}
	configureRootRequestFlags(root)
	registerProjectLinkCommands(root)
	return root
}

func projectLinkTestEnvironment(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("HOME", directory)
	t.Setenv("APPDATA", directory)
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("OPENAI_PROJECT_ID", "")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
	require.NoError(t, os.Unsetenv("OPENAI_PROJECT_ID"))
	t.Chdir(t.TempDir())
	path, err := projectLinkPath()
	require.NoError(t, err)
	return path
}

func TestProjectLinkExplicitFlagDoesNotSaveEnvironment(t *testing.T) {
	path := projectLinkTestEnvironment(t)
	t.Setenv("OPENAI_PROJECT_ID", "proj_environment")
	var out bytes.Buffer
	require.NoError(t, projectLinkTestCommand(&out).Run(t.Context(), []string{"openai", "link"}))
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Contains(t, out.String(), "OPENAI_PROJECT_ID overrides")
	for _, args := range [][]string{
		{"openai", "--project", "proj_explicit", "link"},
		{"openai", "link", "--project", "proj_explicit"},
	} {
		require.NoError(t, projectLinkTestCommand(io.Discard).Run(t.Context(), args))
		links, err := loadProjectLinks(t.Context(), path)
		require.NoError(t, err)
		directory, err := projectLinkDirectory()
		require.NoError(t, err)
		require.Equal(t, "proj_explicit", links[directory])
	}
}

func TestProjectLinkSkipsFirstRunShellSetup(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "link"}, {"openai", "link", "--project", "proj_work"},
		{"openai", "--project=proj_work", "link"}, {"openai", "unlink"},
		{"openai", "help", "link"}, {"openai", "unlink", "--help"},
	} {
		require.False(t, imagePickerFirstRunEligible(args, func(string) string { return "" }, true, true, true, "bash"))
	}
}

type projectLinkBrokenWriter struct{ err error }

func (w projectLinkBrokenWriter) Write([]byte) (int, error) { return 0, w.err }

func TestProjectLinkOutputFailurePreservesCommittedLink(t *testing.T) {
	path := projectLinkTestEnvironment(t)
	failure := errors.New("synthetic output failure")
	err := projectLinkTestCommand(projectLinkBrokenWriter{failure}).Run(t.Context(), []string{"openai", "link", "--project", "proj_saved"})
	require.ErrorIs(t, err, failure)
	links, err := loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	directory, err := projectLinkDirectory()
	require.NoError(t, err)
	require.Equal(t, "proj_saved", links[directory])
}

func TestProjectLinkRejectsModesBeforeMutation(t *testing.T) {
	path := projectLinkTestEnvironment(t)
	for _, flags := range [][]string{{"--format", "yaml"}, {"--transform", "project"}, {"--raw-output=false"}, {"unexpected"}} {
		args := append([]string{"openai", "link", "--project", "proj_work"}, flags...)
		require.Error(t, projectLinkTestCommand(io.Discard).Run(t.Context(), args))
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestProjectLinkCanonicalMissingDirectory(t *testing.T) {
	projectLinkTestEnvironment(t)
	directory := filepath.Join(t.TempDir(), "removed")
	require.NoError(t, os.Mkdir(directory, 0700))
	t.Chdir(directory)
	if err := os.Remove(directory); err != nil {
		t.Skip("host does not allow removing the working directory")
	}
	err := projectLinkTestCommand(io.Discard).Run(t.Context(), []string{"openai", "link", "--project", "proj_work"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "current folder")
}

func TestProjectLinkResolutionFailureDoesNotRetry(t *testing.T) {
	path := projectLinkTestEnvironment(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("malformed"), 0600))
	root := projectLinkTestCommand(io.Discard)
	attempts := 0
	root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, command *cli.Command) error {
		opts := []option.RequestOption{option.WithAPIKey("synthetic-key"), option.WithMiddleware(func(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			attempts++
			return next(request)
		})}
		opts = append(opts, projectLinkRequestOptions(command)...)
		client := openai.NewClient(opts...)
		_, err := client.Models.List(ctx)
		return err
	}})
	err := root.Run(t.Context(), []string{"openai", "probe"})
	require.ErrorContains(t, err, "Could not resolve folder project defaults")
	require.Equal(t, 1, attempts)
}

func TestProjectLinkResolutionPreservesCommandCancellation(t *testing.T) {
	projectLinkTestEnvironment(t)
	root := projectLinkTestCommand(io.Discard)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	root.Commands = append(root.Commands, &cli.Command{Name: "probe", Action: func(ctx context.Context, command *cli.Command) error {
		cancel()
		opts := projectLinkRequestOptions(command)
		require.Len(t, opts, 2, "canceled lookup must produce the deterministic failure options")
		client := openai.NewClient(append(opts, option.WithAPIKey("synthetic-key"))...)
		_, err := client.Models.List(ctx)
		return err
	}})
	err := root.Run(ctx, []string{"openai", "probe"})
	require.ErrorIs(t, err, context.Canceled)
}
