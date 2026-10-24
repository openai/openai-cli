package models

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestModelsListLimitHelpDefaultKeepsPresence(t *testing.T) {
	for _, test := range []struct {
		args  []string
		set   bool
		value int64
	}{
		{nil, false, 0},
		{[]string{"--max-items", "0"}, true, 0},
		{[]string{"--max-items", "-1"}, true, -1},
	} {
		limit := &requestflag.Flag[int64]{Name: "max-items"}
		unrelated := &requestflag.Flag[int64]{Name: "max-items"}
		called := false
		command := modelsOptionsCommand(func(_ context.Context, command *cli.Command) error {
			called = true
			require.Equal(t, test.set, command.IsSet("max-items"))
			require.Equal(t, test.value, command.Value("max-items"))
			return nil
		})
		command.Command("models").Command("list").Flags = []cli.Flag{limit}
		command.Command("files").Command("list").Flags = []cli.Flag{unrelated}
		Configure(command, Options{})
		require.Equal(t, "unlimited", limit.GetDefaultText())
		require.Zero(t, limit.Default)
		require.Empty(t, unrelated.DefaultText)
		require.NoError(t, command.Run(t.Context(), append([]string{"openai", "models", "list"}, test.args...)))
		require.True(t, called)
		require.Equal(t, "unlimited", limit.GetDefaultText())
	}
}

func TestModelsListOptionsAreScopedAndPreserveContext(t *testing.T) {
	for _, test := range []struct {
		args                 []string
		selected, descending bool
	}{
		{[]string{"models", "list"}, false, false},
		{[]string{"models", "list", "--sort-by", "~id"}, true, true},
		{[]string{"models", "list", "--filter", ` id = "synthetic value" `}, true, false},
	} {
		called := false
		ctx, cancel := context.WithCancel(t.Context())
		command := modelsOptionsCommand(func(got context.Context, _ *cli.Command) error {
			called = true
			options, selected := got.Value(modelsListOptionsKey{}).(modelsListOptions)
			require.Equal(t, test.selected, selected)
			require.Equal(t, test.descending, options.selection.Descending)
			cancel()
			require.ErrorIs(t, got.Err(), context.Canceled)
			return nil
		})
		Configure(command, Options{})
		require.NoError(t, command.Run(ctx, append([]string{"openai"}, test.args...)))
		require.True(t, called)
		for _, flag := range command.Command("files").Command("list").Flags {
			require.NotContains(t, flag.Names(), "filter")
			require.NotContains(t, flag.Names(), "sort-by")
		}
	}
}

func modelsOptionsCommand(action cli.ActionFunc) *cli.Command {
	return &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "format"}},
		Commands: []*cli.Command{
			{Name: "models", Commands: []*cli.Command{{Name: "list", Action: action}}},
			{Name: "files", Commands: []*cli.Command{{Name: "list"}}},
		},
	}
}

func TestModelsListFilterOperandRemainsExact(t *testing.T) {
	for _, value := range []string{" leading and trailing ", "value=with=equals", `"literal quotes"`, "model AND other", `single' and "double" quotes`, `model\path`, "model\nline", "\x00\xff"} {
		quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
		options, err := parseModelsListOptions("id="+quoted, true, "id")
		require.NoError(t, err)
		require.NotNil(t, options.selection.ExactID)
		require.Equal(t, value, *options.selection.ExactID)
	}
	options, err := parseModelsListOptions("id~(?i)^model$", true, "id")
	require.NoError(t, err)
	require.True(t, options.selection.Pattern.MatchString("MODEL"))
	require.False(t, options.selection.Pattern.MatchString("prefix-MODEL"))
}
