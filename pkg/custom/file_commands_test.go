package custom

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func fileWorkflowCommandTree(action cli.ActionFunc) *cli.Command {
	return &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{{
		Name: "files", Commands: []*cli.Command{
			{Name: "create", Action: action, Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "file", Required: true, BodyPath: "file", FileInput: true},
				&requestflag.Flag[string]{Name: "purpose", Required: true, BodyPath: "purpose"},
				&requestflag.Flag[string]{Name: "future", Aliases: []string{"f"}, Default: "preserved", BodyPath: "future"},
			}},
			{Name: "retrieve", Action: action, Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "file-id", Required: true, PathParam: "file_id"},
			}},
			{Name: "content", Action: action, Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "file-id", Required: true, PathParam: "file_id"},
				&requestflag.Flag[string]{Name: "output", Aliases: []string{"o"}},
			}},
		},
	}}}
}

func TestFileCommandsPreserveGeneratedFlagsAndDecoratedHooks(t *testing.T) {
	root := fileWorkflowCommandTree(func(context.Context, *cli.Command) error { return nil })
	files := root.Command("files")
	for _, name := range []string{"create", "retrieve", "content"} {
		source := files.Command(name)
		source.Metadata = map[string]any{"future": "preserved"}
		source.Before = func(ctx context.Context, _ *cli.Command) (context.Context, error) { return ctx, nil }
		source.After = func(context.Context, *cli.Command) error { return nil }
		source.ShellComplete = func(context.Context, *cli.Command) {}
	}
	configureTaskCommands(root)
	configureFileCommands(root)
	for _, pair := range [][2]string{{"create", "upload"}, {"retrieve", "get"}, {"content", "download"}} {
		source, friendly := files.Command(pair[0]), files.Command(pair[1])
		require.NotSame(t, source, friendly)
		require.Len(t, friendly.Flags, len(source.Flags))
		for i := range source.Flags {
			require.Same(t, source.Flags[i], friendly.Flags[i], "%s flag identity", pair[1])
		}
		require.Equal(t, reflect.ValueOf(source.Before).Pointer(), reflect.ValueOf(friendly.Before).Pointer())
		require.Equal(t, reflect.ValueOf(source.After).Pointer(), reflect.ValueOf(friendly.After).Pointer())
		require.Equal(t, reflect.ValueOf(source.ShellComplete).Pointer(), reflect.ValueOf(friendly.ShellComplete).Pointer())
		require.Equal(t, "preserved", friendly.Metadata["future"])
		friendly.Metadata["future"] = "changed"
		require.Equal(t, "preserved", source.Metadata["future"])
	}
	file := files.Command("upload").Flags[0].(*requestflag.Flag[string])
	purpose := files.Command("upload").Flags[1].(*requestflag.Flag[string])
	future := files.Command("upload").Flags[2].(*requestflag.Flag[string])
	require.True(t, file.Required)
	require.True(t, file.FileInput)
	require.Equal(t, "file", file.BodyPath)
	require.True(t, purpose.Required)
	require.Empty(t, purpose.Default)
	require.Equal(t, "preserved", future.Default)
	require.Equal(t, []string{"f"}, future.Aliases)
	require.Empty(t, files.Command("create").Arguments)
	for _, name := range []string{"upload", "get", "download"} {
		require.False(t, files.Command(name).Hidden)
	}
}

func TestFileUploadParsedArgumentsAndActionPassthrough(t *testing.T) {
	for _, tc := range []struct {
		name, wantFile string
		args           []string
		wantError      string
	}{
		{"quoted path", "upload space.txt", []string{"upload space.txt", "--purpose", "user_data"}, ""},
		{"literal at", "@upload space.txt", []string{"@upload space.txt", "--purpose", "user_data"}, ""},
		{"literal dash", "-notes.txt", []string{"--purpose", "user_data", "--", "-notes.txt"}, ""},
		{"legacy flag", "upload space.txt", []string{"--file", "upload space.txt", "--purpose", "user_data"}, ""},
		{"legacy stdin", "-", []string{"--file", "-", "--purpose", "user_data"}, ""},
		{"path and flag", "", []string{"one", "--file", "two", "--purpose", "user_data"}, "Use either"},
		{"path and empty flag", "", []string{"one", "--file=", "--purpose", "user_data"}, "Use either"},
		{"empty positional", "", []string{"", "--purpose", "user_data"}, "must not be empty"},
		{"extra positional", "", []string{"one", "two", "--purpose", "user_data"}, "Unexpected extra arguments"},
		{"options after delimiter", "", []string{"--", "one", "--purpose", "user_data"}, "Unexpected extra arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			failure := errors.New("decorated handler result")
			root := fileWorkflowCommandTree(func(ctx context.Context, command *cli.Command) error {
				calls++
				require.Equal(t, fileUploadCommand, ctx.Value(fileCommandKey{}))
				require.Equal(t, tc.wantFile, command.String("file"))
				require.True(t, command.IsSet("file"))
				require.Equal(t, "user_data", command.String("purpose"))
				require.Empty(t, command.Args().Slice(), "the generated action must not receive consumed paths")
				return failure
			})
			configureTaskCommands(root)
			configureFileCommands(root)
			err := root.Run(t.Context(), append([]string{"openai", "files", "upload"}, tc.args...))
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Zero(t, calls)
			} else {
				require.ErrorIs(t, err, failure)
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestFileCommandsKeepPurposeValidationAndOldRoutes(t *testing.T) {
	for _, route := range []string{"upload", "create", "get", "retrieve", "download", "content"} {
		t.Run(route, func(t *testing.T) {
			calls := 0
			failure := errors.New("decorated handler failure")
			root := fileWorkflowCommandTree(func(ctx context.Context, command *cli.Command) error {
				calls++
				wantMarker := map[string]string{"upload": fileUploadCommand, "get": fileGetCommand}[route]
				marker, _ := ctx.Value(fileCommandKey{}).(string)
				require.Equal(t, wantMarker, marker)
				if route == "upload" || route == "create" {
					missing := requestflag.GetMissingRequiredFlags(command, nil)
					require.Len(t, missing, 1)
					require.Equal(t, "purpose", missing[0].Names()[0])
				} else {
					require.Equal(t, []string{"file-example"}, command.Args().Slice())
				}
				return failure
			})
			configureTaskCommands(root)
			configureFileCommands(root)
			args := []string{"openai", "files", route, "file-example"}
			if route == "create" {
				args = []string{"openai", "files", route, "--file", "file-example"}
			}
			require.ErrorIs(t, root.Run(t.Context(), args), failure)
			require.Equal(t, 1, calls)
		})
	}
}

func TestFileCommandsPreserveExistingFriendlyRoutes(t *testing.T) {
	root := fileWorkflowCommandTree(func(context.Context, *cli.Command) error { return nil })
	files := root.Command("files")
	get, download := &cli.Command{Name: "get"}, &cli.Command{Name: "download"}
	files.Commands = append(files.Commands, get, download)
	configureTaskCommands(root)
	configureFileCommands(root)
	require.Same(t, get, files.Command("get"))
	require.Same(t, download, files.Command("download"))
	require.False(t, files.Command("retrieve").Hidden)
	require.False(t, files.Command("content").Hidden)
	require.NotPanics(t, func() { configureFileCommands(&cli.Command{Name: "openai"}) })
}

func TestFileCommandsMakeWorkflowDiscoverable(t *testing.T) {
	root := fileWorkflowCommandTree(func(context.Context, *cli.Command) error { return nil })
	files := root.Command("files")
	files.Commands = append(files.Commands, &cli.Command{Name: "list"}, &cli.Command{Name: "delete"})
	configureTaskCommands(root)
	configureFileCommands(root)
	require.Equal(t, "Upload, inspect, and download files.", files.Usage)
	var names []string
	for _, command := range clihelp.VisibleCommands(files) {
		names = append(names, command.Name)
	}
	require.Equal(t, []string{"upload", "get", "download", "list", "delete"}, names)
	for _, name := range []string{"create", "retrieve", "content"} {
		require.NotNil(t, files.Command(name))
	}
}
