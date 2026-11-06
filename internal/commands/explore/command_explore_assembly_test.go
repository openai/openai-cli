package explore_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/commands/explore"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestExploreBrowsesInstalledCommandsAlphabetically(t *testing.T) {
	root := commands.Command
	app.ConfigureCommand(root)
	_, _, err := app.ConfigureHelp(root, []string{"openai", "explore"})
	require.NoError(t, err)
	candidates := commandsuggest.Search(explore.CandidatesForTest(root, "openai", "openai", "bash"), "")
	var commands []string
	for _, candidate := range candidates {
		commands = append(commands, candidate.Command)
	}

	previous := -1
	for _, command := range []string{
		"admin --help", "audio --help", "batches --help", "files list",
		"images models", "--format json images models", "models list", "tokenizer count",
	} {
		index := slices.Index(commands, "openai "+command)
		require.Greater(t, index, previous, "expected %q after index %d", command, previous)
		previous = index
	}
}

func TestEveryTaskPhraseFindsItsInstalledCommandFirst(t *testing.T) {
	root := commands.Command
	app.ConfigureCommand(root)
	_, _, err := app.ConfigureHelp(root, []string{"openai", "explore"})
	require.NoError(t, err)
	candidates := explore.CandidatesForTest(root, "openai", "openai", "bash")

	for path, phrases := range explore.TaskPhrasesForTest {
		t.Run(path, func(t *testing.T) {
			target := root
			for _, name := range strings.Split(path, " ") {
				target = target.Command(name)
				require.NotNil(t, target, "unknown command in task phrase registry: %s", path)
				require.Equal(t, name, target.Name, "use the canonical command path")
				require.False(t, target.Hidden, "hidden command in task phrase registry: %s", path)
			}
			for _, phrase := range phrases {
				t.Run(phrase.Text, func(t *testing.T) {
					require.NotEmpty(t, strings.TrimSpace(phrase.Text), "task phrase cannot be blank")
					matches := commandsuggest.Search(candidates, phrase.Text)
					require.NotEmpty(t, matches, "no results for %q", phrase.Text)
					require.Equal(t, "openai "+path, strings.TrimSuffix(matches[0].Command, " --help"),
						"first result for %q", phrase.Text)
				})
			}
		})
	}
}

func TestExploreDiscoversEveryVisibleRoute(t *testing.T) {
	root := commands.Command
	app.ConfigureCommand(root)
	_, _, err := app.ConfigureHelp(root, []string{"openai", "explore"})
	require.NoError(t, err)
	catalog := explore.CandidatesForTest(root, "openai", "openai", "zsh")
	phrases := map[string]string{"create": "make a new", "list": "enumerate", "retrieve": "look up", "update": "modify", "delete": "remove"}
	var routes, operations int
	first := func(query, want string) {
		t.Helper()
		matches := commandsuggest.Search(catalog, query)
		require.NotEmpty(t, matches, "search for %q", query)
		require.Equal(t, want, matches[0].Command, "first result for %q", query)
	}
	var walk func(*cli.Command, string)
	walk = func(parent *cli.Command, path string) {
		for _, child := range parent.VisibleCommands() {
			name := strings.TrimSpace(path + " " + child.Name)
			if child.Metadata["help-command-section"] != "Retired commands" {
				want := "openai " + name
				if len(child.VisibleCommands()) > 0 {
					want += " --help"
				}
				first(name, want)
				routes++
				phrase := phrases[child.Name]
				if path != "" && phrase != "" {
					first(phrase+" "+strings.ReplaceAll(path, "-", " "), want)
					operations++
				}
			}
			walk(child, name)
		}
	}
	walk(root, "")
	require.Positive(t, operations)
	t.Logf("checked %d visible routes and %d resource operations", routes, operations)
}

func BenchmarkExploreSearchInstalledCatalog(b *testing.B) {
	root := commands.Command
	app.ConfigureCommand(root)
	_, _, err := app.ConfigureHelp(root, []string{"openai", "explore"})
	require.NoError(b, err)
	candidates := explore.CandidatesForTest(root, "openai", "openai", "zsh")
	for _, query := range []string{"models list", "speech composer", "upload mime type", "list files in a vector store", "file expiration policy", "unknown nonsense"} {
		b.Run(query, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				commandsuggest.Search(candidates, query)
			}
		})
	}
}
