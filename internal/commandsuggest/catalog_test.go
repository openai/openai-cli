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
			return []Candidate{{Command: `openai models list --format json`, Description: "Print JSON"}}
		}
		return nil
	}
	children := func(c *cli.Command) []*cli.Command { return c.VisibleCommands() }
	got := Catalog(root, "openai", nil, children, examples)
	require.Equal(t, []Candidate{{Command: "openai models --help", path: "models"}, {Command: "openai models list", Description: "List available models", path: "models list"}, {Command: "openai models list --format json", Description: "Print JSON"}}, got)
	root.Commands = append(root.Commands, &cli.Command{Name: "future"})
	require.Equal(t, "openai future", Catalog(root, "openai", nil, children, examples)[3].Command)
	require.False(t, called)
}

func TestCatalogRanksTaskPhrasesOnInstalledRoutes(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{
		{Name: "read", Commands: []*cli.Command{{Name: "aloud"}}},
		{Name: "speak"},
		{Name: "hidden", Hidden: true},
	}}
	// A help example can introduce a command before the catalog visits its route.
	examples := func(_ *cli.Command, path string) []Candidate {
		if path != "" {
			return nil
		}
		return []Candidate{
			{Command: "openai speak", Description: "Synthesize audio"},
			{Command: "openai speak --help", Description: "Read aloud options"},
		}
	}
	phrases := map[string][]string{"speak": {"read aloud"}, "hidden": {"read aloud"}}
	catalog := Catalog(root, "openai", phrases, (*cli.Command).VisibleCommands, examples)

	commands := func(query string) []string {
		var result []string
		for _, candidate := range Search(catalog, query) {
			result = append(result, candidate.Command)
		}
		return result
	}
	require.Equal(t, []string{"openai read aloud", "openai speak", "openai speak --help"}, commands("read aloud"))
	require.Equal(t, []string{"openai speak", "openai read aloud", "openai speak --help"}, commands("read alo"))
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
	got := Catalog(root, "openai", nil, func(c *cli.Command) []*cli.Command { return c.VisibleCommands() },
		func(_ *cli.Command, path string) []Candidate {
			if path == "retired" {
				return []Candidate{{Command: "openai replacement --name example", Description: "Replacement operation"}}
			}
			return nil
		})
	require.Equal(t, []Candidate{{Command: "openai replacement --name example", Description: "Replacement operation"}, {Command: "openai replacement", path: "replacement"}}, got)
}
