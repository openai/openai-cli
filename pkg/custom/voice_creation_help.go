package custom

import (
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

// Decorate the generated resource before subgroup cloning so both command
// spellings retain the same setup guidance and executable example.
func configureVoiceCreationHelp(root *cli.Command) {
	voices := root.Command("audio:voices")
	if voices == nil {
		return
	}
	if create := voices.Command("create"); create != nil {
		setCompleteHelpContent(create, clihelp.Content{
			Description: "Requires custom-voice access and a sample from the same speaker as the consent recording.\n" +
				"First upload the consent recording through the API. Replace cons_demo with the returned consent ID.\n" +
				"Replace sample.wav with your sample recording.\n" +
				"Consent setup: https://developers.openai.com/api/docs/guides/custom-voices",
			Examples: []clihelp.Example{{
				Description: "Create a voice from your sample and uploaded consent:",
				Command:     "audio voices create --name Demo --consent cons_demo --audio-sample sample.wav",
			}},
		})
	}
}
