package audio_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

func buildAudioRequestOptions(command *cli.Command) ([]option.RequestOption, error) {
	return custom.FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, custom.ApplicationJSON, true)
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
