package commandsuggest

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestCommandSearchRanksNamesBeforeDescriptionsAndExamples(t *testing.T) {
	candidates := []Candidate{
		{"openai models --help", "List and inspect available models"},
		{"openai files list", "List files used with models"},
		{"openai models list --format json", "Return JSON"},
		{"openai models list", "List available models"},
		{"openai models retrieve", "Retrieve a model"},
	}
	for _, query := range []string{"models list", "list models", "MoDeLs LiSt", "openai models list", "  models   list  "} {
		t.Run(query, func(t *testing.T) {
			matches := searchCandidates(candidates, query)
			require.Equal(t, []int{3, 2, 0, 1}, matches)
			require.Equal(t, []Candidate{candidates[3], candidates[2], candidates[0], candidates[1]}, Search(candidates, query))
		})
	}
	require.Equal(t, []int{2}, searchCandidates(candidates, "--format json"))
	require.Equal(t, []int{0, 1, 2, 3, 4}, searchCandidates(candidates, "  "))
}

func TestCommandSearchRejectsUnrelatedFragmentsAndURLMatches(t *testing.T) {
	candidates := []Candidate{
		{"openai transcribe", "Convert audio to text"},
		{"openai projects --help", "Manage organization projects"},
		{"openai images inline on", "Enable automatic image previews"},
		{"openai files list", "See https://example.test/api/files for details"},
		{"openai help setup", "Configure your API key"},
		{"openai responses retrieve", "Retrieve a response with the given ID"},
		{"openai responses create", "Return [JSON](https://example.test/api/json) or [image output](https://example.test/api/images)"},
	}
	for _, tc := range []struct {
		query string
		want  []int
	}{
		{"on", []int{2}}, {"api", []int{4}}, {"key", []int{4}},
		{"response id", []int{5}}, {"json", []int{6}}, {"image output", []int{6}}, {"ap", nil},
		{"scribe", nil}, {"zzzz", nil}, {"models delete", nil}, {"-", []int{1}},
	} {
		t.Run(tc.query, func(t *testing.T) { require.Equal(t, tc.want, searchCandidates(candidates, tc.query)) })
	}
}

func TestCommandSearchPrefixesPunctuationAndLiteralSelection(t *testing.T) {
	for _, tc := range []struct{ command, description, query string }{
		{"openai models list", "", "model"},
		{"openai vector-stores list", "", "vector store"},
		{"'/tmp/my cli/openai' models list", "", "models list"},
		{"openai models list", "Show available IDs", "models available"},
		{`openai future --name 'İK'`, "", "ik"},
		{`openai future --name 'é'`, "", "e"},
		{`openai future --name '👍🏽'`, "", "👍"},
		{`openai future --name '$(do-not-run)'`, "", "future"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			m := newCommandPicker([]Candidate{{tc.command, tc.description}}, "Commands", true)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m.Update(tea.PasteMsg{Content: tc.query})
			require.Equal(t, []int{0}, m.matches)
			require.Empty(t, m.result)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, tc.command, m.result)
		})
	}
}

func TestCommandSearchStableTiesAndEditingSelection(t *testing.T) {
	candidates := []Candidate{{"openai files list", ""}, {"openai files show", ""}}
	m := newCommandPicker(candidates, "Commands", true)
	m.Update(tea.PasteMsg{Content: "files"})
	require.Equal(t, []int{0, 1}, m.matches)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, m.selected)
	m.Update(tea.PasteMsg{Content: " list"})
	require.Equal(t, []int{0}, m.matches)
	require.Zero(t, m.selected)
}
