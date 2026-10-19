package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImagePickerShellSetupInstallRemoveExplicitProfile(t *testing.T) {
	images.CheckTestImagePickerShellSetupInstallRemoveExplicitProfile(assemblyForTest(), t)
}
func TestImagePickerShellSetupRejectsPowerShellWithoutWriting(t *testing.T) {
	images.CheckTestImagePickerShellSetupRejectsPowerShellWithoutWriting(assemblyForTest(), t)
}
func TestImagePickerShellSetupValidationPreservesRecoveryAndExit(t *testing.T) {
	images.CheckTestImagePickerShellSetupValidationPreservesRecoveryAndExit(assemblyForTest(), t)
}
func TestImagePickerShellSetupAutomaticPowerShellIsQuietAndDoesNotWrite(t *testing.T) {
	images.CheckTestImagePickerShellSetupAutomaticPowerShellIsQuietAndDoesNotWrite(assemblyForTest(), t)
}
func TestImagePickerShellSetupBashDefaultUpdatesBothStartupModes(t *testing.T) {
	images.CheckTestImagePickerShellSetupBashDefaultUpdatesBothStartupModes(assemblyForTest(), t)
}
func TestImagePickerShellSetupBashRemovalAfterLoginPrecedenceChanges(t *testing.T) {
	images.CheckTestImagePickerShellSetupBashRemovalAfterLoginPrecedenceChanges(assemblyForTest(), t)
}
func TestImagePickerShellSetupBashExplicitRemovalKeepsOtherProfiles(t *testing.T) {
	images.CheckTestImagePickerShellSetupBashExplicitRemovalKeepsOtherProfiles(assemblyForTest(), t)
}
func TestImagePickerShellSetupPartialFailureIsRetryable(t *testing.T) {
	images.CheckTestImagePickerShellSetupPartialFailureIsRetryable(assemblyForTest(), t)
}
func TestImagePickerShellSetupBashRemovalContinuesAfterUnrelatedFailure(t *testing.T) {
	images.CheckTestImagePickerShellSetupBashRemovalContinuesAfterUnrelatedFailure(assemblyForTest(), t)
}
func TestImagePickerShellSetupRemovalRetainsPreferenceAndCleanupFailures(t *testing.T) {
	images.CheckTestImagePickerShellSetupRemovalRetainsPreferenceAndCleanupFailures(assemblyForTest(), t)
}
func TestImagePickerShellSetupRejectsInvalidActionCombinations(t *testing.T) {
	images.CheckTestImagePickerShellSetupRejectsInvalidActionCombinations(assemblyForTest(), t)
}
func TestImagePickerShellSetupUnknownShellAndAutomaticHomeDoNotWrite(t *testing.T) {
	images.CheckTestImagePickerShellSetupUnknownShellAndAutomaticHomeDoNotWrite(assemblyForTest(), t)
}
func TestImagePickerShellSetupCanceledBeforeActionDoesNotWrite(t *testing.T) {
	images.CheckTestImagePickerShellSetupCanceledBeforeActionDoesNotWrite(assemblyForTest(), t)
}
func TestImagePickerShellSetupCancellationWhileAnotherSetupHoldsLock(t *testing.T) {
	images.CheckTestImagePickerShellSetupCancellationWhileAnotherSetupHoldsLock(assemblyForTest(), t)
}
func TestImagePickerShellSetupPreservesCompletionOutput(t *testing.T) {
	images.CheckTestImagePickerShellSetupPreservesCompletionOutput(assemblyForTest(), t)
}
