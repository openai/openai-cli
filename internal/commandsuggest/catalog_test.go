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
	require.Len(t, got, 3)
	require.Equal(t, "openai models --help", got[0].Command)
	require.Equal(t, "models", got[0].path)
	require.Equal(t, "openai models list", got[1].Command)
	require.Equal(t, "models list", got[1].path)
	require.Equal(t, "List available models", got[1].Description)
	require.Equal(t, "openai models list --format json", got[2].Command)
	require.Equal(t, "Print JSON", got[2].Description)
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
	phrases := map[string][]TaskPhrase{"speak": {{Text: "read aloud"}}, "hidden": {{Text: "read aloud"}}}
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
	require.Len(t, got, 2)
	require.Equal(t, "openai replacement --name example", got[0].Command)
	require.Equal(t, "Replacement operation", got[0].Description)
	require.Equal(t, "openai replacement", got[1].Command)
	require.Equal(t, "replacement", got[1].path)
}

func TestBrowseUsesCommandNamesAndPreservesRecentOrder(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{{Name: "zebra"}, {Name: "Banana"}, {Name: "apple"}}}
	const native = "& '/tmp/a b/openai'"
	const bash = "'/tmp/a b/openai'"
	examples := func(_ *cli.Command, path string) []Candidate {
		if path == "Banana" {
			return []Candidate{
				Example(bash, "zebra --output file", "Print a zebra"),
				Example(bash, "--format json Banana", "Print a banana"),
			}
		}
		return nil
	}
	catalog := Catalog(root, native, nil, (*cli.Command).VisibleCommands, examples)
	picker := newCommandPicker(catalog, "Explore commands", false)
	var commands []string
	for _, i := range picker.matches {
		commands = append(commands, catalog[i].Command)
	}
	require.Equal(t, []string{
		native + " apple", native + " Banana", bash + " --format json Banana",
		native + " zebra", bash + " zebra --output file",
	}, commands)

	recent, err := ReadCandidates(strings.NewReader("openai zebra\nopenai apple\n"))
	require.NoError(t, err)
	require.Equal(t, []int{0, 1}, newCommandPicker(recent, "Recent commands", true).matches)
}
