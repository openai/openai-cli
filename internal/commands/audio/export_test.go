package audio

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"
)

// TextContextForTest supplies captured bytes to assembly contracts without a production setter.
func TextContextForTest(ctx context.Context, value string) context.Context {
	capture := &audioTextBody{present: true}
	capture.WriteString(value)
	return context.WithValue(ctx, audioTextKey{}, capture)
}

var CheckAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags = checkAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags

var CheckAudioGenerationSendsSelectedRequestSettings = checkAudioGenerationSendsSelectedRequestSettings

var CheckAudioActionsGenerationStoresOnlySentInstructions = checkAudioActionsGenerationStoresOnlySentInstructions

// GenerateFreshSpeechForTest submits the composer through the generated speech action.
func GenerateFreshSpeechForTest(t *testing.T, ctx context.Context, cmd *cli.Command, next cli.ActionFunc) error {
	t.Helper()
	workbench := newAudioWorkbench(ctx, cmd, next, audioTestStore(t))
	workbench.input.SetValue("Synthetic speech")
	return audioSeekWorker(t, workbench.generate(false)).err
}
