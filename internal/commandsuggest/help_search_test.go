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
	catalog := Catalog(root, invocation, nil, fixtureHelp, (*cli.Command).VisibleCommands, func(_ *cli.Command, path string) []Candidate {
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
	catalog := Catalog(root, "openai", nil, fixtureHelp, (*cli.Command).VisibleCommands, func(_ *cli.Command, _ string) []Candidate { return nil })
	index := newSearchIndex(catalog)
	require.Empty(t, index.search("paint share")) // Both "shade" and "shape" are one edit away.
	require.Empty(t, index.search("paint banana"))
	require.NotEmpty(t, index.search("paint studio"))
	require.Equal(t, "openai speak", catalog[index.search("speak")[0]].Command)

	// The recent-command picker has no installed definitions.
	recent := []Candidate{{Command: "openai paint"}}
	require.Empty(t, newSearchIndex(recent).search("paint studio"))
}

// This tree has no task phrases or command-specific search configuration.
func TestDiscoveryFromNewCommandDefinitions(t *testing.T) {
	leaf := func(name, usage string) *cli.Command { return &cli.Command{Name: name, Usage: usage} }
	polish := leaf("polish", "Finish a prism.")
	polish.Aliases = []string{"buff"}
	polish.Flags = []cli.Flag{&cli.StringFlag{Name: "grit", Usage: "Choose the abrasive coarseness."}}
	prisms := &cli.Command{Name: "prisms", Commands: []*cli.Command{
		polish, leaf("list", "Browse inventory."), leaf("delete", "Discard one object."),
		{Name: "facets", Commands: []*cli.Command{leaf("delete", "Remove a prism facet."), leaf("list", "Browse facets.")}},
	}}
	root := &cli.Command{Commands: []*cli.Command{prisms, leaf("weave", "Convert strands into ribbons."), leaf("unweave", "Produce strands from ribbons.")}}
	catalog := Catalog(root, "openai", nil, fixtureHelp, (*cli.Command).VisibleCommands, func(_ *cli.Command, _ string) []Candidate { return nil })
	index := newSearchIndex(catalog)
	for query, want := range map[string]string{
		"polish a prism": "prisms polish", "buff a prism": "prisms polish",
		"prisms abrasive coarseness": "prisms polish", "show me all prisms": "prisms list",
		"remove a prism": "prisms delete", "remove a facet in a prism": "prisms facets delete",
		"show facets in a prism":    "prisms facets list",
		"turn strands into ribbons": "weave", "convert ribbons to strands": "unweave",
	} {
		matches := index.search(query)
		require.NotEmpty(t, matches, query)
		require.Equal(t, "openai "+want, catalog[matches[0]].Command, query)
	}
	require.Empty(t, index.search("polish an asteroid"))
	require.Empty(t, index.search("coarseness"), "a passing mention in option help needs a subject")
	fresh := newSearchIndex(catalog)
	require.Equal(t, index.search("prisms abrasive coarseness"), fresh.search("prisms abrasive coarseness"), "prior searches must not affect results")
	matches := index.search("turn strands into ribbons")
	for _, i := range matches {
		require.NotEqual(t, "openai unweave", catalog[i].Command)
	}
}

func TestOptionSearchKeepsSharedWordsDistinct(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{
		{Name: "paint", Usage: "Adjust the spectrum.", Flags: []cli.Flag{
			&cli.StringFlag{Name: "mode", Usage: "Set the spectrum contrast."},
		}},
		{Name: "measure", Usage: "Inspect the spectrum."},
	}}
	catalog := Catalog(root, "openai", nil, fixtureHelp, (*cli.Command).VisibleCommands, func(_ *cli.Command, _ string) []Candidate { return nil })
	search := newSearchIndex(catalog)
	for _, query := range []string{"paint contrast", "paint spectrm contrast", "spectrum contr"} {
		matches := search.search(query)
		require.Len(t, matches, 1, query)
		require.Equal(t, "openai paint", catalog[matches[0]].Command, query)
	}
	require.Equal(t, newSearchIndex(catalog).search("spectrum"), search.search("spectrum"))
}

func TestFinalPrefixSurvivesUnrelatedExactWord(t *testing.T) {
	narrate := &cli.Command{Name: "narrate", Usage: "Generate narration.", Description: "Open the terminal form.",
		Flags: []cli.Flag{&cli.StringFlag{Name: "voice", Usage: "Controls orchestration and orchestrated recordings."}}}
	compose := &cli.Command{Name: "compose", Usage: "Arrange stories."}
	root := &cli.Command{Commands: []*cli.Command{narrate, compose}}
	catalog := Catalog(root, "openai", nil, fixtureHelp, (*cli.Command).VisibleCommands, func(_ *cli.Command, _ string) []Candidate { return nil })
	index := newSearchIndex(catalog)
	for _, q := range []string{"voice compos", "voice compose", "voice composer"} {
		t.Run(q, func(t *testing.T) {
			hits := index.search(q)
			require.Len(t, hits, 1)
			require.Equal(t, "openai narrate", catalog[hits[0]].Command)
		})
	}
	// An exact hit in the option tier still beats a partial hit in primary help.
	tune := &cli.Command{Name: "tune", Flags: []cli.Flag{
		&cli.StringFlag{Name: "voice", Usage: "Choose compose or mix."},
	}}
	root.Commands = append(root.Commands, tune, &cli.Command{Name: "orchestrate"})
	catalog = Catalog(root, "openai", nil, fixtureHelp, (*cli.Command).VisibleCommands, func(_ *cli.Command, _ string) []Candidate { return nil })
	index = newSearchIndex(catalog)
	for _, tc := range []struct{ query, want string }{
		{"voice compose", "openai tune"},
		{"voice orchestrate", "openai narrate"}, // Prefix exists only in option help.
	} {
		hits := index.search(tc.query)
		require.Len(t, hits, 1, tc.query)
		require.Equal(t, tc.want, catalog[hits[0]].Command, tc.query)
	}
	require.Empty(t, index.search("voice co"))
	require.Empty(t, index.search("voice orchestration nonexistent"))

}
