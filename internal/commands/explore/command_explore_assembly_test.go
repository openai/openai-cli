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
)

func TestExploreBrowsesInstalledCommandsAlphabetically(t *testing.T) {
	root := commands.Command
	app.ConfigureCommand(root)
	_, _, err := app.ConfigureHelp(root, []string{"openai", "explore"})
	require.NoError(t, err)
	candidates := commandsuggest.Search(explore.CandidatesForTest(root, "openai", "bash"), "")
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
	candidates := explore.CandidatesForTest(root, "openai", "bash")

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
