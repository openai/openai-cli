//go:build darwin

package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImagePickerMacOSShellConfigChangesShareDecisionLock(t *testing.T) {
	images.CheckTestImagePickerMacOSShellConfigChangesShareDecisionLock(assemblyForTest(), t)
}
func TestImagePickerMacOSShellSetupRetainsLegacyScriptRoot(t *testing.T) {
	images.CheckTestImagePickerMacOSShellSetupRetainsLegacyScriptRoot(assemblyForTest(), t)
}
func TestImagePickerMacOSFishOptOutAcrossConfigurationRoots(t *testing.T) {
	images.CheckTestImagePickerMacOSFishOptOutAcrossConfigurationRoots(assemblyForTest(), t)
}
