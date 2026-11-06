package audio_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/commands/audio"
	appconfig "github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

func buildAudioRequestOptions(command *cli.Command) ([]option.RequestOption, error) {
	return appconfig.FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, appconfig.ApplicationJSON, true)
}

func TestAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags(t *testing.T) {
	audio.CheckAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags(t, buildAudioRequestOptions)
}

func TestAudioGenerationSendsSelectedRequestSettings(t *testing.T) {
	audio.CheckAudioGenerationSendsSelectedRequestSettings(t, buildAudioRequestOptions)
}

func TestAudioActionsGenerationStoresOnlySentInstructions(t *testing.T) {
	audio.CheckAudioActionsGenerationStoresOnlySentInstructions(t, buildAudioRequestOptions)
}
