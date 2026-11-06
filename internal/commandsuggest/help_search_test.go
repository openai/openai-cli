package commandsuggest

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestExploreSearchesPublicHelpAndOptions(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{
		{
			Name: "narrate", Usage: "Generate narration.", Description: "Open the terminal form. [Reference](https://private.example/fixturelink)\n    openai narrate --text exampleoperand",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "voice", Value: "sampledefault"},
				&cli.StringFlag{Name: "hiddenoption", Hidden: true},
			},
		},
		{Name: "library", Usage: "Browse recordings.", Description: "Choose a recording from the terminal picker."},
		{Name: "hiddenroute", Hidden: true, Description: "Open the terminal form.", Usage: "Generate narration."},
	}}
	const invocation = "'/a b/openai'"
	catalog := Catalog(root, invocation, nil, (*cli.Command).VisibleCommands, func(_ *cli.Command, path string) []Candidate {
		if path == "" {
			// An example can register the displayed command before its installed route.
			return []Candidate{Example(invocation, "narrate", "Read aloud")}
		}
		return nil
	})
	index := newSearchIndex(catalog)
	require.NotEmpty(t, index.search("narrate"))
	require.Nil(t, index.help, "literal searches should not build the help index")
	for _, query := range []string{"narration composer", "voice editor", "voice compos", "narration compser", "narrtion composer"} {
		matches := index.search(query)
		require.Len(t, matches, 1, query)
		require.Equal(t, invocation+" narrate", catalog[matches[0]].Command)
	}
	for _, query := range []string{
		"library composer", "narration unknownword", "narration fixturelink", "narration exampleoperand",
		"narration hiddenoption", "narration sampledefault", "hiddenroute composer", "narrtion compser",
		"voice --composer", "voice $(composer)",
	} {
		require.Empty(t, index.search(query), query)
	}
	picker := newCommandPicker(catalog, "Explore", false)
	picker.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	picker.Update(tea.PasteMsg{Content: "voice editor"})
	picker.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, invocation+" narrate", picker.result)
}

func TestHelpSearchKeepsAmbiguousSpellingsAndLiteralRoutesDistinct(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{
		{Name: "paint", Usage: "Adjust colors.", Description: "Open the terminal form to choose a shade or shape."},
		{Name: "speak"},
	}}
	catalog := Catalog(root, "openai", nil, (*cli.Command).VisibleCommands, func(_ *cli.Command, _ string) []Candidate { return nil })
	index := newSearchIndex(catalog)
	require.Empty(t, index.search("paint share")) // Both "shade" and "shape" are one edit away.
	require.Empty(t, index.search("paint banana"))
	require.NotEmpty(t, index.search("paint studio"))
	require.Equal(t, "openai speak", catalog[index.search("speak")[0]].Command)

	// The recent-command picker has no installed definitions.
	recent := []Candidate{{Command: "openai paint"}}
	require.Empty(t, newSearchIndex(recent).search("paint studio"))
}
