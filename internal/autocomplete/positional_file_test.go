package autocomplete

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func positionalFileCompletionTree(t *testing.T) *cli.Command {
	t.Helper()
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&cli.StringFlag{Name: "project"},
		&cli.StringFlag{Name: "file", TakesFile: true},
	}, Commands: []*cli.Command{
		{Name: "files", Commands: []*cli.Command{
			{Name: "upload", Metadata: map[string]any{"completion-positional-file": "file"}, Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "file", Aliases: []string{"f"}, FileInput: true},
				&requestflag.Flag[string]{Name: "purpose", Aliases: []string{"p"}},
			}},
			{Name: "create", Flags: []cli.Flag{&requestflag.Flag[string]{Name: "file", FileInput: true}}},
			{Name: "get", Flags: []cli.Flag{&requestflag.Flag[string]{Name: "file-id"}}},
		}},
	}}
	_, _, err := clihelp.Configure(root, []string{"openai"})
	require.NoError(t, err)
	return root
}

func TestPositionalFileCompletionOnlyFirstEligiblePath(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish, CompletionStylePowershell} {
		for _, tc := range []struct {
			args []string
			want ShellCompletionBehavior
		}{
			{[]string{"files", "upload", ""}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "upload sp"}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "@literal"}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "a=b"}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "--purpose", "user_data", ""}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "--purpose=user_data", ""}, ShellCompletionBehaviorFile},
			{[]string{"--project", "help", "files", "upload", ""}, ShellCompletionBehaviorFile},
			{[]string{"files", "--project", "upload", "upload", ""}, ShellCompletionBehaviorFile},
			{[]string{"--file", "root-only.txt", "files", "upload", ""}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "--", "-literal"}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "--", "--file=x"}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "--", ""}, ShellCompletionBehaviorFile},
			{[]string{"files", "upload", "first.txt", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "first.txt", "second.txt", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "first.txt", "--purpose", "user_data", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--", "first.txt", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--", "--file=x", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--file", "chosen.txt", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--file=chosen.txt", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "-f", "chosen.txt", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "-f=chosen.txt", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--file=", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--file", "", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--file", "--", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--file=one", "--file=two", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "upload", "--unknown", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"files", "missing", ""}, ShellCompletionBehaviorNoComplete},
			{[]string{"missing", "files", "upload", ""}, ShellCompletionBehaviorNoComplete},
		} {
			t.Run(string(style)+"/"+strings.Join(tc.args, "|"), func(t *testing.T) {
				got := GetCompletions(style, positionalFileCompletionTree(t), tc.args)
				require.Equal(t, tc.want, got.Behavior)
				require.Empty(t, got.Completions)
				require.Empty(t, got.FileValuePrefix)
				if tc.want == ShellCompletionBehaviorFile {
					require.True(t, got.requiresFileValueSupport)
				}
			})
		}
	}
}

func TestPositionalFileCompletionPreservesOtherCompletion(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		want   ShellCompletionBehavior
		prefix string
		names  []string
	}{
		{args: []string{"files", "upload", "--p"}, names: []string{"--purpose", "--project"}},
		{args: []string{"files", "upload", "first.txt", "--p"}, names: []string{"--purpose", "--project"}},
		{args: []string{"files", "upload", "--purpose", ""}, want: ShellCompletionBehaviorNoComplete},
		{args: []string{"files", "upload", "first.txt", "--purpose", ""}, want: ShellCompletionBehaviorNoComplete},
		{args: []string{"files", "upload", "--file", ""}, want: ShellCompletionBehaviorFile},
		{args: []string{"files", "upload", "first.txt", "--file", ""}, want: ShellCompletionBehaviorFile},
		{args: []string{"files", "upload", "--file", "--file=literal"}, want: ShellCompletionBehaviorFile},
		{args: []string{"files", "upload", "--file=assets/a"}, want: ShellCompletionBehaviorFile, prefix: "--file="},
		{args: []string{"files", "upload", "first.txt", "-f=assets/a"}, want: ShellCompletionBehaviorFile, prefix: "-f="},
		{args: []string{"files", "upload", "--purpose=assets/a"}, want: ShellCompletionBehaviorNoComplete},
		{args: []string{"files", "create", ""}},
		{args: []string{"files", "create", "--file", ""}, want: ShellCompletionBehaviorFile},
		{args: []string{"files", "get", "file-example", ""}},
		{args: []string{"help", "files", "upload", ""}},
		{args: []string{"files", "help", "upload", ""}},
		{args: []string{"help", "--all", "files", "upload", ""}},
		{args: []string{"help", "--all=false", "files", "upload", ""}},
		{args: []string{"help", "--all=bad", "files", "upload", ""}, want: ShellCompletionBehaviorNoComplete},
	} {
		t.Run(strings.Join(tc.args, "|"), func(t *testing.T) {
			got := GetCompletions(CompletionStyleBash, positionalFileCompletionTree(t), tc.args)
			require.Equal(t, tc.want, got.Behavior)
			require.Equal(t, tc.prefix, got.FileValuePrefix)
			require.ElementsMatch(t, tc.names, completionNames(got.Completions))
		})
	}
}

func TestPositionalFileCompletionMetadataRequiresFileFlag(t *testing.T) {
	for _, metadata := range []any{nil, true, "", "missing", "purpose"} {
		root := positionalFileCompletionTree(t)
		upload := root.Command("files").Command("upload")
		upload.Metadata["completion-positional-file"] = metadata
		got := GetCompletions(CompletionStyleBash, root, []string{"files", "upload", ""})
		require.NotEqual(t, ShellCompletionBehaviorFile, got.Behavior, "%v", metadata)
	}
}

func TestPositionalFileCompletionProtocolCapabilityGate(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, capability := range []string{"", "other-adapter", "1"} {
			t.Run(style+"/"+capability, func(t *testing.T) {
				t.Setenv("COMPLETION_STYLE", style)
				t.Setenv("OPENAI_CLI_COMPLETION_FILE_VALUES", capability)
				root := positionalFileCompletionTree(t)
				var output bytes.Buffer
				root.Writer = &output
				root.SkipFlagParsing = true
				root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
				root.Commands = append(root.Commands, &cli.Command{Name: "__complete", Hidden: true, SkipFlagParsing: true, Action: ExecuteShellCompletion})
				args := []string{"openai", "__complete"}
				if style != "zsh" {
					args = append(args, "--")
				}
				args = append(args, "files", "upload", "a=b")
				err := root.Run(t.Context(), args)
				var exit cli.ExitCoder
				require.ErrorAs(t, err, &exit)
				want := ShellCompletionBehaviorNoComplete
				if capability == "1" {
					want = ShellCompletionBehaviorFile
				}
				require.Equal(t, int(want), exit.ExitCode())
				require.Empty(t, output.String())
			})
		}
	}
}
