package custom

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePickerPackageCommandContract(t *testing.T) {
	home := pickerShellSetupHome(t)
	for _, shell := range []string{"fish"} {
		output, err := runPickerShellSetup(t, t.Context(), shell, "--package-picker")
		require.NoError(t, err)
		require.Contains(t, output, "/usr/bin/openai")
		require.Contains(t, output, "image-picker.json.tab-off-"+shell)
		require.Contains(t, output, "OPENAI_PICKER_INTEGRATION")
	}
	for _, args := range [][]string{
		{"--package-picker"}, {"pwsh", "--package-picker"}, {"bash", "--package-picker"}, {"zsh", "--package-picker"},
		{"fish", "--package-picker", "--install-picker"},
		{"fish", "--package-picker", "--automatic"},
		{"fish", "--package-picker", "--picker"},
		{"fish", "--package-picker", "--uninstall-picker"},
		{"fish", "--package-picker", "--profile", "/tmp/example"},
	} {
		_, err := runPickerShellSetup(t, t.Context(), args...)
		require.Error(t, err, "%v", args)
	}
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries, "package actions must not discover or write user profiles")
}

func TestImagePickerPackageRequestSetupExemption(t *testing.T) {
	for _, args := range [][]string{
		{"@completion", "fish", "--package-picker"},
		{"@completion", "fish", "--package-picker=true"},
		{"@completion", "fish", "--package-picker=1"},
	} {
		require.True(t, IsImagePickerPackageCommand(args), "%v", args)
	}
	for _, args := range [][]string{
		nil, {"openai"}, {"@completion", "zsh"},
		{"images", "generate", "--prompt", "--package-picker"},
		{"@completion", "--package-picker-extra"},
		{"@completion", "fish", "--package-picker=false"},
		{"@completion", "fish", "--package-picker=invalid"},
		{"@completion", "--profile", "--package-picker"},
		{"@completion", "fish", "--", "--package-picker"},
	} {
		require.False(t, IsImagePickerPackageCommand(args), "%v", args)
	}
}
