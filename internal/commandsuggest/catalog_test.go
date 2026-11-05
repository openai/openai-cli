package commandsuggest

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCatalogUsesCurrentDefinitionsWithoutActions(t *testing.T) {
	called := false
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{
		{Name: "models", Commands: []*cli.Command{{Name: "list", Usage: "List available models", Action: func(_ context.Context, _ *cli.Command) error { called = true; return nil }}}},
		{Name: "hidden", Hidden: true}, {Name: "bad;name"}, {Name: "-flag"},
	}}
	examples := func(_ *cli.Command, path string) []Candidate {
		if path == "models list" {
			return []Candidate{{`openai models list --format json`, "Print JSON"}}
		}
		return nil
	}
	children := func(c *cli.Command) []*cli.Command { return c.VisibleCommands() }
	got := Catalog(root, "openai", children, examples)
	require.Equal(t, []Candidate{{"openai models --help", ""}, {"openai models list", "List available models"}, {"openai models list --format json", "Print JSON"}}, got)
	root.Commands = append(root.Commands, &cli.Command{Name: "future"})
	require.Equal(t, "openai future", Catalog(root, "openai", children, examples)[3].Command)
	require.False(t, called)
}

func TestReadCandidatesPreservesQuotesAndRejectsMalformedInput(t *testing.T) {
	literal := `openai responses create --input '$(do-not-run)'`
	got, err := ReadCandidates(strings.NewReader(literal + "\n" + literal + "\n"))
	require.NoError(t, err)
	require.Equal(t, []Candidate{{Command: literal}}, got)
	for _, bad := range []string{"", "\n", "good\n\n", "good\npartial", "unterminated", "good\r\n", "good\n\x1b[2J\n", "x\u2028y", "x\u202ey", string([]byte{0xff}), strings.Repeat("x", maxCommandBytes+1), strings.Repeat("good\n", maxCommands+1)} {
		_, err := ReadCandidates(strings.NewReader(bad))
		require.Error(t, err)
	}
	got, err = ReadCandidates(strings.NewReader(strings.Repeat("x", maxCommandBytes) + "\n"))
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestCatalogRetiredCommandsOfferTheirDeclaredReplacement(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{
		{Name: "retired", Metadata: map[string]any{"help-command-section": "Retired commands"}},
		{Name: "replacement"},
	}}
	got := Catalog(root, "openai", func(c *cli.Command) []*cli.Command { return c.VisibleCommands() },
		func(_ *cli.Command, path string) []Candidate {
			if path == "retired" {
				return []Candidate{{"openai replacement --name example", "Replacement operation"}}
			}
			return nil
		})
	require.Equal(t, []Candidate{{"openai replacement --name example", "Replacement operation"}, {"openai replacement", ""}}, got)
}
