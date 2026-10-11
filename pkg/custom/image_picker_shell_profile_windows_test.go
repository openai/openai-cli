//go:build windows

package custom

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/autocomplete"
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
	output, diagnostic, err := runPickerShellSetupWithDiagnostics(t, t.Context(), "bash", "--install-picker", "--profile", profile)
	require.NoError(t, err)
	require.Empty(t, output)
	require.Equal(t, "Tab setup saved for future terminals. Existing custom bindings are preserved.\n", diagnostic)
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

func TestImagePickerWindowsShellHomeFishUsesConfiguredXDG(t *testing.T) {
	home := pickerShellSetupHome(t)
	config := filepath.Join(home, "fish settings")
	appdata := filepath.Join(home, "native settings")
	t.Setenv("XDG_CONFIG_HOME", filepath.ToSlash(config))
	t.Setenv("APPDATA", appdata)
	profile := filepath.Join(config, "fish", "conf.d", "openai-picker.fish")
	nativeProfile := filepath.Join(appdata, "fish", "conf.d", "openai-picker.fish")
	original := []byte("# existing shell configuration\n")
	for _, path := range []string{profile, nativeProfile} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, original, 0600))
	}
	nativeBefore, err := os.Stat(nativeProfile)
	require.NoError(t, err)
	targets, err := imagePickerShellTarget(t.Context(), "fish", false, "")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, profile, targets[0].Profile)
	require.Equal(t, filepath.Join(appdata, "openai", "shell"), targets[0].Directory)
	for range 2 {
		_, err = runPickerShellSetup(t, t.Context(), "fish", "--install-picker")
		require.NoError(t, err)
		installed, err := os.ReadFile(profile)
		require.NoError(t, err)
		require.Contains(t, string(installed), "# >>> openai image picker")
		active, err := autocomplete.IsPickerInstalled(t.Context(), targets[0])
		require.NoError(t, err)
		require.True(t, active)
	}
	_, err = runPickerShellSetup(t, t.Context(), "fish", "--uninstall-picker")
	require.NoError(t, err)
	restored, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Equal(t, original, restored)
	scripts, err := filepath.Glob(filepath.Join(appdata, "openai", "shell", "picker-fish-*.fish"))
	require.NoError(t, err)
	require.Empty(t, scripts)
	nativeAfter, err := os.Stat(nativeProfile)
	require.NoError(t, err)
	require.True(t, os.SameFile(nativeBefore, nativeAfter), "the AppData fish loader must not be replaced")
	require.Equal(t, nativeBefore.ModTime(), nativeAfter.ModTime())
	unchanged, err := os.ReadFile(nativeProfile)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
}

func TestImagePickerWindowsShellHomeFishFallsBackToHomeConfig(t *testing.T) {
	home := pickerShellSetupHome(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	appdata := filepath.Join(home, "native settings")
	t.Setenv("APPDATA", appdata)
	targets, err := imagePickerShellTarget(t.Context(), "fish", false, "")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, filepath.Join(home, ".config", "fish", "conf.d", "openai-picker.fish"), targets[0].Profile)
	require.Equal(t, filepath.Join(appdata, "openai", "shell"), targets[0].Directory)
	_, err = runPickerShellSetup(t, t.Context(), "fish", "--install-picker")
	require.NoError(t, err)
	active, err := autocomplete.IsPickerInstalled(t.Context(), targets[0])
	require.NoError(t, err)
	require.True(t, active)
	_, err = runPickerShellSetup(t, t.Context(), "fish", "--uninstall-picker")
	require.NoError(t, err)
	active, err = autocomplete.IsPickerInstalled(t.Context(), targets[0])
	require.NoError(t, err)
	require.False(t, active)
	_, err = os.Stat(filepath.Join(appdata, "fish"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestImagePickerWindowsShellHomeFishExplicitProfileKeepsAppDataStorage(t *testing.T) {
	home := pickerShellSetupHome(t)
	shellHome := t.TempDir()
	config := filepath.Join(home, "fish settings")
	appdata := filepath.Join(home, "native settings")
	t.Setenv("HOME", shellHome)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", appdata)
	profile := filepath.Join(shellHome, "custom.fish")
	original := []byte("# explicit shell startup\n")
	require.NoError(t, os.WriteFile(profile, original, 0600))
	_, err := runPickerShellSetup(t, t.Context(), "fish", "--install-picker", "--profile", profile)
	require.NoError(t, err)
	targets, err := imagePickerShellTarget(t.Context(), "fish", false, profile)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, profile, targets[0].Profile)
	require.Equal(t, filepath.Join(appdata, "openai", "shell"), targets[0].Directory)
	active, err := autocomplete.IsPickerInstalled(t.Context(), targets[0])
	require.NoError(t, err)
	require.True(t, active)
	_, err = runPickerShellSetup(t, t.Context(), "fish", "--uninstall-picker", "--profile", profile)
	require.NoError(t, err)
	data, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Equal(t, original, data)
	for _, directory := range []string{config, appdata} {
		_, err := os.Stat(filepath.Join(directory, "fish"))
		require.ErrorIs(t, err, os.ErrNotExist, "an explicit profile must not create a default fish loader")
	}
	// An override does not use the shell's default configuration directory.
	t.Setenv("XDG_CONFIG_HOME", "relative-unused-config")
	_, err = runPickerShellSetup(t, t.Context(), "fish", "--install-picker", "--profile", profile)
	require.NoError(t, err)
	targets, err = imagePickerShellTarget(t.Context(), "fish", false, profile)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(appdata, "openai", "shell"), targets[0].Directory)
	_, err = runPickerShellSetup(t, t.Context(), "fish", "--uninstall-picker", "--profile", profile)
	require.NoError(t, err)
}

func TestImagePickerWindowsShellHomeFishRejectsInvalidXDG(t *testing.T) {
	for _, config := range []string{"relative-config", "/c/Users/synthetic-config", "C:drive-relative", "C:\\synthetic\nconfig"} {
		t.Run(config, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			t.Setenv("XDG_CONFIG_HOME", config)
			targets, err := imagePickerShellTarget(t.Context(), "fish", false, "")
			require.Error(t, err)
			require.Empty(t, targets)
			for _, action := range []string{"--install-picker", "--uninstall-picker"} {
				output, err := runPickerShellSetup(t, t.Context(), "fish", action)
				require.Error(t, err)
				require.Empty(t, output)
			}
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			require.Empty(t, entries, "invalid XDG must not fall back to AppData or create other profiles")
		})
	}
}

func TestImagePickerWindowsShellHomeOtherShellsKeepAppData(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			config := filepath.Join(home, "other XDG settings")
			appdata := filepath.Join(home, "native settings")
			t.Setenv("XDG_CONFIG_HOME", config)
			t.Setenv("APPDATA", appdata)
			targets, err := imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			for _, target := range targets {
				require.Equal(t, filepath.Join(appdata, "openai", "shell"), target.Directory)
			}
			_, err = runPickerShellSetup(t, t.Context(), shell, "--install-picker")
			require.NoError(t, err)
			for _, target := range targets {
				active, err := autocomplete.IsPickerInstalled(t.Context(), target)
				require.NoError(t, err)
				require.True(t, active)
			}
			_, err = runPickerShellSetup(t, t.Context(), shell, "--uninstall-picker")
			require.NoError(t, err)
			_, err = os.Stat(config)
			require.ErrorIs(t, err, os.ErrNotExist)
			t.Setenv("XDG_CONFIG_HOME", "relative-unused-config")
			targets, err = imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			for _, target := range targets {
				require.Equal(t, filepath.Join(appdata, "openai", "shell"), target.Directory)
			}
		})
	}
}

func TestImagePickerWindowsShellHomeFishConfigChangesShareSetupLock(t *testing.T) {
	home := pickerShellSetupHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "fish-a"))
	choice, err := imagePickerTabChoicePath("fish")
	require.NoError(t, err)
	acquired, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- autocomplete.WithPickerSetupLock(t.Context(), filepath.Dir(choice), func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	select {
	case <-acquired:
	case err := <-finished:
		require.NoError(t, err)
		t.Fatal("setup lock callback did not run")
	}
	defer func() {
		close(release)
		require.NoError(t, <-finished)
	}()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "fish-b"))
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	_, err = runPickerShellSetup(t, ctx, "fish", "--uninstall-picker")
	require.ErrorIs(t, err, context.DeadlineExceeded, "another fish configuration must wait on the same decision lock")
	declined, err := imagePickerTabDeclined("fish")
	require.NoError(t, err)
	require.False(t, declined, "a blocked setup decision must not update the shared preference")
	for _, config := range []string{"fish-a", "fish-b"} {
		_, err = os.Stat(filepath.Join(home, config))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestImagePickerWindowsShellHomeFishSharesOptOutAcrossRoots(t *testing.T) {
	home := pickerShellSetupHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "fish-a"))
	_, err := runPickerShellSetup(t, t.Context(), "fish", "--install-picker")
	require.NoError(t, err)
	first, err := imagePickerShellTarget(t.Context(), "fish", false, "")
	require.NoError(t, err)
	path, err := imagePickerTabChoicePath("fish")
	require.NoError(t, err)
	require.Equal(t, path, first[0].OptOutPath)
	profile, err := os.ReadFile(first[0].Profile)
	require.NoError(t, err)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "fish-b"))
	second, err := imagePickerShellTarget(t.Context(), "fish", false, "")
	require.NoError(t, err)
	require.NotEqual(t, first[0].Profile, second[0].Profile)
	require.Equal(t, first[0].OptOutPath, second[0].OptOutPath)
	for _, action := range []string{"--uninstall-picker", "--install-picker"} {
		_, err = runPickerShellSetup(t, t.Context(), "fish", action)
		require.NoError(t, err)
		declined, err := imagePickerTabDeclined("fish")
		require.NoError(t, err)
		require.Equal(t, action == "--uninstall-picker", declined)
		unchanged, err := os.ReadFile(first[0].Profile)
		require.NoError(t, err)
		require.Equal(t, profile, unchanged, "the old root must use the shared preference without a profile rewrite")
	}
	explicit, err := imagePickerShellTarget(t.Context(), "fish", false, filepath.Join(home, "explicit.fish"))
	require.NoError(t, err)
	require.Equal(t, path, explicit[0].OptOutPath)
}
