package autocomplete

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCompletionResolvesCommandAliases(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{{
		Name: "config:get", Aliases: []string{"cfg:get"},
		Flags: []cli.Flag{&cli.StringFlag{Name: "selected-option"}},
	}}}
	for _, name := range []string{"config:get", "cfg:get"} {
		got := GetCompletions(CompletionStyleBash, root, []string{name, "--sel"})
		require.Equal(t, []ShellCompletion{{Name: "--selected-option"}}, got.Completions)
	}
}
