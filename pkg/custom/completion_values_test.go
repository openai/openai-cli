package custom

import (
	"context"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCompletionValuesPreserveFlagOwnership(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&cli.StringFlag{Name: "format", Aliases: []string{"f"}},
		&cli.StringFlag{Name: "format-error"},
	}, Commands: []*cli.Command{
		{Name: "other", Flags: []cli.Flag{&cli.StringFlag{Name: "format"}}},
		{Name: "files", Commands: []*cli.Command{
			{Name: "create", Flags: []cli.Flag{&requestflag.Flag[string]{Name: "purpose", Aliases: []string{"p"}, BodyPath: "purpose"}}},
			{Name: "list", Flags: []cli.Flag{&requestflag.Flag[string]{Name: "purpose", QueryPath: "purpose"}}},
			{Name: "other", Flags: []cli.Flag{&requestflag.Flag[string]{Name: "purpose", BodyPath: "purpose"}}},
		}},
	}}
	configureCompletionValues(root)
	for _, tc := range []struct {
		args []string
		want []autocomplete.ShellCompletion
	}{
		{[]string{"-f", "j"}, []autocomplete.ShellCompletion{{Name: "json"}, {Name: "jsonl"}}},
		{[]string{"--format-error", "j"}, []autocomplete.ShellCompletion{{Name: "json"}, {Name: "jsonl"}}},
		{[]string{"files", "create", "-p", "ba"}, []autocomplete.ShellCompletion{{Name: "batch"}}},
		{[]string{"files", "list", "--purpose", "ba"}, []autocomplete.ShellCompletion{{Name: "batch"}, {Name: "batch_output"}}},
	} {
		got := autocomplete.GetCompletions(autocomplete.CompletionStyleBash, root, tc.args)
		require.Equal(t, autocomplete.ShellCompletionBehaviorDefault, got.Behavior, tc.args)
		require.Equal(t, tc.want, got.Completions, tc.args)
	}
	for _, args := range [][]string{
		{"other", "--format", "j"},
		{"files", "other", "--purpose", "ba"},
		{"files", "create", "--purpose", "batch_output"},
	} {
		got := autocomplete.GetCompletions(autocomplete.CompletionStyleBash, root, args)
		require.EqualValues(t, autocomplete.ShellCompletionBehaviorNoComplete, got.Behavior, args)
		require.Empty(t, got.Completions, args)
	}
}

func TestCompletionValuesLeaveRequestValuesUnrestricted(t *testing.T) {
	purpose := &requestflag.Flag[string]{Name: "purpose", BodyPath: "purpose"}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Commands: []*cli.Command{{Name: "files", Commands: []*cli.Command{{
			Name: "create", Flags: []cli.Flag{purpose},
			Action: func(_ context.Context, command *cli.Command) error {
				require.Equal(t, "future-purpose", command.String("purpose"))
				return nil
			},
		}}}},
	}
	configureCompletionValues(root)
	require.NoError(t, root.Run(context.Background(), []string{"openai", "files", "create", "--purpose", "future-purpose"}))
	require.Nil(t, purpose.Validator)
	require.Equal(t, "purpose", purpose.BodyPath)
}

func TestCompletionValuesSelectedCommandOverrides(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&cli.StringFlag{Name: "format", Aliases: []string{"f"}},
		&cli.StringFlag{Name: "format-error"},
	}, Commands: []*cli.Command{
		{Name: "restricted", Aliases: []string{"r"},
			Metadata: map[string]any{"completion-root-flag-values": map[string][]string{"format": {"json"}}},
			Commands: []*cli.Command{{Name: "child"}},
		},
		{Name: "shadow", Flags: []cli.Flag{&cli.StringFlag{Name: "format"}},
			Metadata: map[string]any{"completion-root-flag-values": map[string][]string{"format": {"json"}}},
		},
		{Name: "empty", Metadata: map[string]any{"completion-root-flag-values": map[string][]string{"format": {}}}},
	}}
	configureCompletionValues(root)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"--format", "j"}, []string{"json", "jsonl"}},
		{[]string{"restricted", "--format", "j"}, []string{"json"}},
		{[]string{"r", "-f=j"}, []string{"json"}},
		{[]string{"--format", "text", "restricted", "--format", "j"}, []string{"json"}},
		{[]string{"restricted", "--format-error", "j"}, []string{"json", "jsonl"}},
		{[]string{"restricted", "child", "--format", "j"}, []string{"json", "jsonl"}},
		{[]string{"shadow", "--format", "j"}, nil},
		{[]string{"empty", "--format", ""}, nil},
	} {
		got := autocomplete.GetCompletions(autocomplete.CompletionStyleBash, root, tc.args)
		var names []string
		for _, completion := range got.Completions {
			names = append(names, completion.Name)
		}
		require.Equal(t, tc.want, names, tc.args)
		if len(tc.want) == 0 {
			require.EqualValues(t, autocomplete.ShellCompletionBehaviorNoComplete, got.Behavior, tc.args)
		} else {
			require.Equal(t, autocomplete.ShellCompletionBehaviorDefault, got.Behavior, tc.args)
		}
	}
}
