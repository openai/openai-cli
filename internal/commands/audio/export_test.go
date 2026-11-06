package audio

import "context"

// TextContextForTest supplies captured bytes to assembly contracts without a production setter.
func TextContextForTest(ctx context.Context, value string) context.Context {
	capture := &audioTextBody{present: true}
	capture.WriteString(value)
	return context.WithValue(ctx, audioTextKey{}, capture)
}

var CheckAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags = checkAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags

var CheckAudioGenerationSendsSelectedRequestSettings = checkAudioGenerationSendsSelectedRequestSettings

var CheckAudioActionsGenerationStoresOnlySentInstructions = checkAudioActionsGenerationStoresOnlySentInstructions
