package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImageInlinePreferenceFailureKeepsPrivatePathsAndOriginal(t *testing.T) {
	images.CheckTestImageInlinePreferenceFailureKeepsPrivatePathsAndOriginal(assemblyForTest(), t)
}
func TestImageInlinePreferenceWarningTerminal(t *testing.T) {
	images.CheckTestImageInlinePreferenceWarningTerminal(assemblyForTest(), t)
}
func TestImagePreviewCommandsRegisterOnceAndRejectFormats(t *testing.T) {
	images.CheckTestImagePreviewCommandsRegisterOnceAndRejectFormats(assemblyForTest(), t)
}
func TestImagePreviewRegistrationPreservesInlineCommands(t *testing.T) {
	images.CheckTestImagePreviewRegistrationPreservesInlineCommands(assemblyForTest(), t)
}
