// Package modeldefaults lists the OpenAI models the CLI selects for the caller.
// Each constant has a corresponding recommendation in scripts/check-model-defaults.py.
package modeldefaults

const (
	Speech                = "gpt-realtime-2.1-mini"
	SavedImage            = "gpt-image-2.6-sunburst"
	Translation           = "gpt-realtime-translate"
	TranslationTranscript = "gpt-realtime-whisper"
	TranslationText       = "gpt-6.1-terra"
)
