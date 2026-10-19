package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImagePickerShellConsentSurvivesConfigurationChanges(t *testing.T) {
	images.CheckTestImagePickerShellConsentSurvivesConfigurationChanges(assemblyForTest(), t)
}
func TestImagePickerShellConsentMigratesCurrentLegacyChoice(t *testing.T) {
	images.CheckTestImagePickerShellConsentMigratesCurrentLegacyChoice(assemblyForTest(), t)
}
func TestImagePickerShellConsentRejectsInvalidLegacyChoice(t *testing.T) {
	images.CheckTestImagePickerShellConsentRejectsInvalidLegacyChoice(assemblyForTest(), t)
}
func TestImagePickerShellConsentDecisionLockAcrossConfigurationChanges(t *testing.T) {
	images.CheckTestImagePickerShellConsentDecisionLockAcrossConfigurationChanges(assemblyForTest(), t)
}
