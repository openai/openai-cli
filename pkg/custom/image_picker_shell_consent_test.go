package custom

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/stretchr/testify/require"
)

func pickerConsentConfig(t *testing.T, home, name string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, name))
	t.Setenv("APPDATA", filepath.Join(home, name))
}

func TestImagePickerShellConsentSurvivesConfigurationChanges(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			pickerConsentConfig(t, home, "config-a")
			for _, action := range []string{"--install-picker", "--uninstall-picker"} {
				_, err := runPickerShellSetup(t, t.Context(), shell, action)
				require.NoError(t, err)
			}
			choice, err := imagePickerTabChoicePath(shell)
			require.NoError(t, err)
			require.Equal(t, filepath.Join(home, ".openai", "shell"), filepath.Dir(choice))
			pickerConsentConfig(t, home, "config-b")
			changedChoice, err := imagePickerTabChoicePath(shell)
			require.NoError(t, err)
			require.Equal(t, choice, changedChoice)
			targets, err := imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			before := firstRunFileSnapshot(t, home)
			for range 2 {
				require.NoError(t, setupImagePickerFirstRun(t.Context(), targets))
			}
			after := firstRunFileSnapshot(t, home)
			require.Len(t, after, len(before), "changing configuration roots must not reinstall declined setup")
			for path, info := range before {
				require.True(t, os.SameFile(info, after[path]))
				require.Equal(t, info.ModTime(), after[path].ModTime())
			}
			_, err = runPickerShellSetup(t, t.Context(), shell, "--install-picker")
			require.NoError(t, err)
			pickerConsentConfig(t, home, "config-a")
			declined, err := imagePickerTabDeclined(shell)
			require.NoError(t, err)
			require.False(t, declined, "explicit re-enable applies across configuration roots")
		})
	}
}

func TestImagePickerShellConsentMigratesCurrentLegacyChoice(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			pickerConsentConfig(t, home, "config-a")
			legacy, err := legacyImagePickerTabChoicePath(shell)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0700))
			require.NoError(t, os.WriteFile(legacy, nil, 0600))
			targets, err := imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			require.NoError(t, setupImagePickerFirstRun(t.Context(), targets))
			choice, err := imagePickerTabChoicePath(shell)
			require.NoError(t, err)
			info, err := os.Lstat(choice)
			require.NoError(t, err)
			require.True(t, privateImagePickerState(info))
			require.Zero(t, info.Size())
			_, err = os.Lstat(legacy)
			require.ErrorIs(t, err, os.ErrNotExist, "migrated consent must not revive after a later re-enable")
			for _, target := range targets {
				_, err := os.Lstat(target.Profile)
				require.ErrorIs(t, err, os.ErrNotExist, "migration must preserve the user's opt-out")
			}
			pickerConsentConfig(t, home, "config-b")
			targets, err = imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			require.NoError(t, setupImagePickerFirstRun(t.Context(), targets))
			_, err = runPickerShellSetup(t, t.Context(), shell, "--install-picker")
			require.NoError(t, err)
			pickerConsentConfig(t, home, "config-a")
			declined, err := imagePickerTabDeclined(shell)
			require.NoError(t, err)
			require.False(t, declined)
		})
	}
}

func TestImagePickerShellConsentRejectsInvalidLegacyChoice(t *testing.T) {
	for _, kind := range []string{"content", "directory", "public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" && (kind == "public" || kind == "symlink") {
				t.Skip("Unix mode or symlink semantics")
			}
			home := pickerShellSetupHome(t)
			legacy, err := legacyImagePickerTabChoicePath("zsh")
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0700))
			switch kind {
			case "directory":
				require.NoError(t, os.Mkdir(legacy, 0700))
			case "symlink":
				target := filepath.Join(home, "unrelated")
				require.NoError(t, os.WriteFile(target, nil, 0600))
				require.NoError(t, os.Symlink(target, legacy))
			default:
				data := []byte(nil)
				if kind == "content" {
					data = []byte("preserve external content")
				}
				require.NoError(t, os.WriteFile(legacy, data, 0600))
				if kind == "public" {
					require.NoError(t, os.Chmod(legacy, 0644))
				}
			}
			before, err := os.Lstat(legacy)
			require.NoError(t, err)
			targets, err := imagePickerShellTarget(t.Context(), "zsh", false, "")
			require.NoError(t, err)
			require.Error(t, setupImagePickerFirstRun(t.Context(), targets))
			_, err = runPickerShellSetup(t, t.Context(), "zsh", "--install-picker")
			require.Error(t, err)
			after, err := os.Lstat(legacy)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			require.Equal(t, before.ModTime(), after.ModTime())
			_, err = os.Stat(targets[0].Profile)
			require.ErrorIs(t, err, os.ErrNotExist)
			choice, err := imagePickerTabChoicePath("zsh")
			require.NoError(t, err)
			_, err = os.Lstat(choice)
			require.ErrorIs(t, err, os.ErrNotExist, "invalid legacy state must not be silently migrated")
		})
	}
}

func TestImagePickerShellConsentDecisionLockAcrossConfigurationChanges(t *testing.T) {
	for _, action := range []string{"first run", "remove"} {
		t.Run(action, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			pickerConsentConfig(t, home, "config-a")
			choice, err := imagePickerTabChoicePath("zsh")
			require.NoError(t, err)
			acquired, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
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
				t.Fatalf("could not hold consent lock: %v", err)
			}
			defer func() { close(release); require.NoError(t, <-finished) }()
			pickerConsentConfig(t, home, "config-b")
			targets, err := imagePickerShellTarget(t.Context(), "zsh", false, "")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			if action == "first run" {
				err = setupImagePickerFirstRun(ctx, targets)
			} else {
				_, err = runPickerShellSetup(t, ctx, "zsh", "--uninstall-picker")
			}
			require.ErrorIs(t, err, context.DeadlineExceeded)
			for _, path := range []string{choice, targets[0].Profile} {
				_, err := os.Lstat(path)
				require.ErrorIs(t, err, os.ErrNotExist, "a blocked decision must not mutate consent or startup")
			}
		})
	}
}
