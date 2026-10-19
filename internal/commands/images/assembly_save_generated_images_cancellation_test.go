package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestSavedImageCancellationPreservesRecovery(t *testing.T) {
	images.CheckTestSavedImageCancellationPreservesRecovery(assemblyForTest(), t)
}
func TestSavedImageCancellationWithoutCompletedFiles(t *testing.T) {
	images.CheckTestSavedImageCancellationWithoutCompletedFiles(assemblyForTest(), t)
}
func TestSavedImageFailureWithoutCompletedFilesKeepsRecovery(t *testing.T) {
	images.CheckTestSavedImageFailureWithoutCompletedFilesKeepsRecovery(assemblyForTest(), t)
}
func TestImagePreferenceCancellationKeepsPathPrivate(t *testing.T) {
	images.CheckTestImagePreferenceCancellationKeepsPathPrivate(assemblyForTest(), t)
}
func TestSavedImageCancellationOutputModes(t *testing.T) {
	images.CheckTestSavedImageCancellationOutputModes(assemblyForTest(), t)
}
func TestSavedImageCancellationAfterReceiptKeepsSavedResult(t *testing.T) {
	images.CheckTestSavedImageCancellationAfterReceiptKeepsSavedResult(assemblyForTest(), t)
}
