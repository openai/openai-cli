package commandsuggest

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func searchCandidates(candidates []Candidate, query string) []int {
	return newSearchIndex(candidates).search(query)
}

func TestCommandSearchRanksNamesBeforeDescriptionsAndExamples(t *testing.T) {
	candidates := []Candidate{
		{Command: "openai models --help", Description: "List and inspect available models"},
		{Command: "openai files list", Description: "List files used with models"},
		{Command: "openai models list --format json", Description: "Return JSON"},
		{Command: "openai models list", Description: "List available models"},
		{Command: "openai models retrieve", Description: "Retrieve a model"},
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
		{Command: "openai transcribe", Description: "Convert audio to text"},
		{Command: "openai projects --help", Description: "Manage organization projects"},
		{Command: "openai images inline on", Description: "Enable automatic image previews"},
		{Command: "openai files list", Description: "See https://example.test/api/files for details"},
		{Command: "openai help setup", Description: "Configure your API key"},
		{Command: "openai responses retrieve", Description: "Retrieve a response with the given ID"},
		{Command: "openai responses create", Description: "Return [JSON](https://example.test/api/json) or [image output](https://example.test/api/images)"},
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

func TestCommandSearchReordersOnlyDeclaredIndependentWords(t *testing.T) {
	candidates := []Candidate{
		{Command: "openai inventory", path: "inventory", taskPhrases: []TaskPhrase{{Text: "Count stored files", AnyWordOrder: true}}},
		{Command: "openai archive", path: "archive", taskPhrases: []TaskPhrase{{Text: "file count stored locally"}}},
		{Command: "openai audit", path: "audit", taskPhrases: []TaskPhrase{{Text: "file count stored"}}},
		{Command: "openai file count", path: "file count"},
		{Command: "openai check", Description: "Check each stored file count"},
	}
	for _, tc := range []struct {
		query string
		want  []int
	}{
		{"file count stored", []int{2, 1, 0, 4}}, // Exact phrase, ordered prefix, reordered terms, usual keywords.
		{"file count", []int{3, 1, 2, 0, 4}},     // The literal route wins.
		{"stored files co", []int{0}},
		{"stored file", []int{0, 4}},
		{"files files count", nil},
		{"stored file count remotely", nil},
		{"stored fil count", []int{4}}, // An unfinished middle word only qualifies for usual keyword search.
		{"files", []int{3}},            // Singular and plural forms match installed route words.
	} {
		t.Run(tc.query, func(t *testing.T) {
			require.Equal(t, tc.want, searchCandidates(candidates, tc.query))
		})
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
			m := newCommandPicker([]Candidate{{Command: tc.command, Description: tc.description}}, "Commands", true)
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
	candidates := []Candidate{{Command: "openai files list", Description: ""}, {Command: "openai files show", Description: ""}}
	m := newCommandPicker(candidates, "Commands", true)
	m.Update(tea.PasteMsg{Content: "files"})
	require.Equal(t, []int{0, 1}, m.matches)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, m.selected)
	m.Update(tea.PasteMsg{Content: " list"})
	require.Equal(t, []int{0}, m.matches)
	require.Zero(t, m.selected)
}

func TestSearchUsesCatalogRoutesForRecovery(t *testing.T) {
	candidates := []Candidate{
		{path: "projects model-permissions retrieve", commandText: "projects model-permissions retrieve"},
		{path: "models retrieve", commandText: "models retrieve"},
		{path: "models list", commandText: "models list", Command: "'/preview path/cli' models list"},
		{commandText: "models list --format json", Command: "'/preview path/cli' models list --format json"},
		{path: "speak", commandText: "speak", taskPhrases: []TaskPhrase{{Text: "text to speech"}}},
		{path: "transcribe", commandText: "transcribe", taskPhrases: []TaskPhrase{{Text: "speech to text"}}},
	}
	index := newSearchIndex(candidates)
	for _, tc := range []struct {
		query string
		want  []int
	}{
		{"retrieve model", []int{1, 0}},
		{"modles list", []int{2}},
		{"models ls", []int{2}},
		{"ls models", []int{2}},
		{"list all models", []int{2, 3}},
		{"retrieve all models", nil},
		{"openai modles list", []int{2}},
		{"modles lsit", nil},
		{"how do I list my models?", []int{2, 3}},
		{"text-to-speech", []int{4}},
		{"speech-to-text", []int{5}},
		{"--fromat", nil},
		{"preview", nil},
		{"---", nil},
		{"my", nil},
	} {
		t.Run(tc.query, func(t *testing.T) { require.Equal(t, tc.want, index.search(tc.query)) })
	}
	// Selection must preserve the executable quoting even after typo recovery.
	require.Equal(t, "'/preview path/cli' models list", Search(candidates, "modles list")[0].Command)
}

func TestConversionSearchRequiresTheCompleteOrderedTask(t *testing.T) {
	index := newSearchIndex([]Candidate{
		{path: "wrap", taskPhrases: []TaskPhrase{{Text: "paper to parcel"}}},
		{path: "unwrap", taskPhrases: []TaskPhrase{{Text: "parcel to paper"}}},
	})
	require.Equal(t, []int{0}, index.search("turn the paper into a parcel"))
	require.Equal(t, []int{1}, index.search("convert parcel to paper"))
	require.Empty(t, index.search("convert paper into parcel overnight"))
	require.Empty(t, index.search("convert into parcel"))
}

func TestReorderedTaskPhrasesRequireTwoContentWords(t *testing.T) {
	phrase := TaskPhrase{Text: "make an image", AnyWordOrder: true}.indexed()
	require.Equal(t, reorderedTask, phrase.match("make image", []string{"make", "image"}))
	require.Equal(t, noTaskMatch, phrase.match("an image", []string{"an", "image"}))
	require.Equal(t, noTaskMatch, phrase.match("image a", []string{"image", "a"}))
}
