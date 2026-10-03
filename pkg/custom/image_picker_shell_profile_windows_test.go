//go:build windows

package custom

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePickerWindowsShellHomeDefaultRefusesAmbiguousPaths(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, action := range []string{"--install-picker", "--uninstall-picker"} {
			for _, variant := range []string{"other native home", "relative home", "MSYS home", "invalid home"} {
				t.Run(shell+"/"+action+"/"+variant, func(t *testing.T) {
					profileHome := pickerShellSetupHome(t)
					shellHome := t.TempDir()
					for _, home := range []string{profileHome, shellHome} {
						require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# personal startup\n"), 0600))
					}
					home := shellHome
					switch variant {
					case "relative home":
						home = "relative-home"
					case "MSYS home":
						home = "/c/Users/shell-home"
					case "invalid home":
						home += "\nother"
					}
					t.Setenv("HOME", home)
					targets, err := imagePickerShellTarget(t.Context(), shell, false, "")
					require.ErrorContains(t, err, "--profile")
					require.Empty(t, targets)
					output, err := runPickerShellSetup(t, t.Context(), shell, action)
					require.ErrorContains(t, err, "--profile PATH")
					require.Empty(t, output)
					for _, home := range []string{profileHome, shellHome} {
						data, err := os.ReadFile(filepath.Join(home, ".bashrc"))
						require.NoError(t, err)
						require.Equal(t, "# personal startup\n", string(data))
						entries, err := os.ReadDir(home)
						require.NoError(t, err)
						require.Len(t, entries, 1, "ambiguous setup must not create profiles, scripts or preferences")
					}
				})
			}
		}
	}
}

func TestImagePickerWindowsShellHomeAutomaticSkipsMismatch(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			profileHome := pickerShellSetupHome(t)
			shellHome := t.TempDir()
			t.Setenv("HOME", shellHome)
			t.Setenv("SHELL", filepath.Join(profileHome, shell+".exe"))
			// This is also the target selection used by first-run setup.
			targets, err := imagePickerShellTarget(t.Context(), shell, true, "")
			require.NoError(t, err)
			require.Empty(t, targets)
			output, err := runPickerShellSetup(t, t.Context(), "--install-picker", "--automatic")
			require.NoError(t, err)
			require.Empty(t, output)
			for _, home := range []string{profileHome, shellHome} {
				entries, err := os.ReadDir(home)
				require.NoError(t, err)
				require.Empty(t, entries)
			}
		})
	}
}

func TestImagePickerWindowsShellHomeExplicitOverride(t *testing.T) {
	profileHome := pickerShellSetupHome(t)
	shellHome := t.TempDir()
	t.Setenv("HOME", shellHome)
	profile := filepath.Join(shellHome, ".bashrc")
	otherProfile := filepath.Join(profileHome, ".bashrc")
	original := []byte("# shell startup\n")
	require.NoError(t, os.WriteFile(profile, original, 0600))
	require.NoError(t, os.WriteFile(otherProfile, []byte("# unrelated native profile\n"), 0600))
	output, err := runPickerShellSetup(t, t.Context(), "bash", "--install-picker", "--profile", profile)
	require.NoError(t, err)
	require.Contains(t, output, "saved for future terminals")
	installed, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Contains(t, string(installed), "# >>> openai image picker")
	_, err = runPickerShellSetup(t, t.Context(), "bash", "--uninstall-picker", "--profile", profile)
	require.NoError(t, err)
	restored, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Equal(t, original, restored)
	unchanged, err := os.ReadFile(otherProfile)
	require.NoError(t, err)
	require.Equal(t, "# unrelated native profile\n", string(unchanged))
	for _, home := range []string{profileHome, shellHome} {
		_, err := os.Stat(filepath.Join(home, ".bash_profile"))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestImagePickerWindowsShellHomeMatchingNativePaths(t *testing.T) {
	for _, format := range []string{"native", "forward slashes", "upper case", "lower case", "unset"} {
		t.Run(format, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			if format == "forward slashes" {
				t.Setenv("HOME", filepath.ToSlash(home))
			} else if format == "upper case" {
				t.Setenv("HOME", strings.ToUpper(home))
			} else if format == "lower case" {
				t.Setenv("HOME", strings.ToLower(home))
			} else if format == "unset" {
				t.Setenv("HOME", "")
			}
			targets, err := imagePickerShellTarget(t.Context(), "bash", false, "")
			require.NoError(t, err)
			require.Len(t, targets, 2)
			require.Equal(t, filepath.Join(home, ".bashrc"), targets[0].Profile)
			require.Equal(t, filepath.Join(home, ".bash_profile"), targets[1].Profile)
		})
	}
}

func TestImagePickerWindowsShellHomeAutomaticUsesDirectoryIdentity(t *testing.T) {
	home := t.TempDir()
	current := &user.User{Uid: "1000", HomeDir: home}
	for _, path := range []string{strings.ToUpper(home), strings.ToLower(home), filepath.ToSlash(home)} {
		require.NoError(t, imagePickerAutomaticHome(path, current, 1000, false))
	}
	require.Error(t, imagePickerAutomaticHome(t.TempDir(), current, 1000, false))
	require.Error(t, imagePickerAutomaticHome(filepath.Join(home, "absent"), current, 1000, false))
}
