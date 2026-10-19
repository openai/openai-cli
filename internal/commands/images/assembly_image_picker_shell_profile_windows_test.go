//go:build windows

package images_test

import (
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
)

func TestImagePickerWindowsShellHomeDefaultRefusesAmbiguousPaths(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeDefaultRefusesAmbiguousPaths(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeAutomaticSkipsMismatch(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeAutomaticSkipsMismatch(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeExplicitOverride(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeExplicitOverride(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeFishUsesConfiguredXDG(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeFishUsesConfiguredXDG(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeFishFallsBackToHomeConfig(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeFishFallsBackToHomeConfig(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeFishExplicitProfileKeepsAppDataStorage(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeFishExplicitProfileKeepsAppDataStorage(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeFishRejectsInvalidXDG(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeFishRejectsInvalidXDG(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeOtherShellsKeepAppData(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeOtherShellsKeepAppData(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeFishConfigChangesShareSetupLock(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeFishConfigChangesShareSetupLock(assemblyForTest(), t)
}
func TestImagePickerWindowsShellHomeFishSharesOptOutAcrossRoots(t *testing.T) {
	images.CheckTestImagePickerWindowsShellHomeFishSharesOptOutAcrossRoots(assemblyForTest(), t)
}
