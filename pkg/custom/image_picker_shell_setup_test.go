package custom

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func pickerShellSetupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for name, value := range map[string]string{
		"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "APPDATA": filepath.Join(home, "config"), "ZDOTDIR": home,
	} {
		t.Setenv(name, value)
	}
	return home
}

func runPickerShellSetup(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	var output, diagnostics bytes.Buffer
	root := &cli.Command{
		Name: "openai", Writer: &output, ErrWriter: &diagnostics,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands:       []*cli.Command{{Name: "@completion", Action: autocomplete.OutputCompletionScript}},
	}
	configureImagePickerCompletion(root)
	configureImagePickerShellSetup(root)
	err := root.Run(ctx, append([]string{"openai", "@completion"}, args...))
	return output.String(), err
}

func TestImagePickerShellSetupInstallRemoveExplicitProfile(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			profile := filepath.Join(home, "startup with spaces")
			original := []byte("# user-owned startup content\n")
			require.NoError(t, os.WriteFile(profile, original, 0600))
			require.NoError(t, declineImagePickerTab(t.Context(), shell))
			for range 2 {
				output, err := runPickerShellSetup(t, t.Context(), shell, "--install-picker", "--profile", profile)
				require.NoError(t, err)
				require.Contains(t, output, "for future terminals")
				targets, err := imagePickerShellTarget(t.Context(), shell, false, profile)
				require.NoError(t, err)
				installed, err := autocomplete.IsPickerInstalled(t.Context(), targets[0])
				require.NoError(t, err)
				require.True(t, installed)
				data, err := os.ReadFile(profile)
				require.NoError(t, err)
				require.True(t, bytes.HasPrefix(data, original))
				require.Equal(t, 1, strings.Count(string(data), "# >>> openai image picker"))
				declined, err := imagePickerTabDeclined(shell)
				require.NoError(t, err)
				require.False(t, declined, "explicit installation must clear an old opt-out")
			}
			for range 2 {
				output, err := runPickerShellSetup(t, t.Context(), shell, "--uninstall-picker", "--profile", profile)
				require.NoError(t, err)
				require.Contains(t, output, "setup removed")
				data, err := os.ReadFile(profile)
				require.NoError(t, err)
				require.Equal(t, original, data)
				declined, err := imagePickerTabDeclined(shell)
				require.NoError(t, err)
				require.True(t, declined, "removal must keep automatic setup off")
			}
		})
	}
}

func TestImagePickerShellSetupRejectsPowerShellWithoutWriting(t *testing.T) {
	home := pickerShellSetupHome(t)
	profile := filepath.Join(home, "profile.ps1")
	for _, action := range []string{"--install-picker", "--uninstall-picker"} {
		for _, explicit := range []bool{false, true} {
			args := []string{"pwsh", action}
			if explicit {
				args = append(args, "--profile", profile)
			}
			output, err := runPickerShellSetup(t, t.Context(), args...)
			var exit cli.ExitCoder
			require.ErrorAs(t, err, &exit)
			require.Equal(t, 2, exit.ExitCode())
			require.Contains(t, err.Error(), "PowerShell uses normal Tab completion")
			require.Contains(t, err.Error(), "openai images generate")
			require.Empty(t, output)
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			require.Empty(t, entries)
		}
	}
}

func TestImagePickerShellSetupAutomaticPowerShellIsQuietAndDoesNotWrite(t *testing.T) {
	home := pickerShellSetupHome(t)
	t.Setenv("SHELL", filepath.Join(home, "pwsh"))
	output, err := runPickerShellSetup(t, t.Context(), "--install-picker", "--automatic")
	require.NoError(t, err)
	require.Empty(t, output)
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestImagePickerShellSetupBashDefaultUpdatesBothStartupModes(t *testing.T) {
	home := pickerShellSetupHome(t)
	profile := filepath.Join(home, ".profile")
	require.NoError(t, os.WriteFile(profile, []byte("# existing login startup\n"), 0600))
	_, err := runPickerShellSetup(t, t.Context(), "bash", "--install-picker")
	require.NoError(t, err)
	for _, path := range []string{profile, filepath.Join(home, ".bashrc")} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), "# >>> openai image picker")
	}
	_, err = os.Stat(filepath.Join(home, ".bash_profile"))
	require.ErrorIs(t, err, os.ErrNotExist, "creating .bash_profile would hide the existing login startup")
}

func TestImagePickerShellSetupBashRemovalAfterLoginPrecedenceChanges(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		t.Run(fmt.Sprintf("reinstall=%t", reinstall), func(t *testing.T) {
			home := pickerShellSetupHome(t)
			originals := map[string][]byte{
				".bashrc":       []byte("# interactive startup\n"),
				".profile":      []byte("# original login startup\n"),
				".bash_login":   []byte("# newer login startup\n"),
				".bash_profile": []byte("# preferred login startup\n"),
			}
			require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), originals[".bashrc"], 0600))
			for _, name := range []string{".profile", ".bash_login", ".bash_profile"} {
				require.NoError(t, os.WriteFile(filepath.Join(home, name), originals[name], 0600))
				if name == ".profile" || reinstall {
					_, err := runPickerShellSetup(t, t.Context(), "bash", "--install-picker")
					require.NoError(t, err)
				}
			}
			unmanaged, err := os.Stat(filepath.Join(home, ".bash_profile"))
			require.NoError(t, err)
			for range 2 {
				output, err := runPickerShellSetup(t, t.Context(), "bash", "--uninstall-picker")
				require.NoError(t, err)
				require.Contains(t, output, "setup removed")
				for name, original := range originals {
					data, err := os.ReadFile(filepath.Join(home, name))
					require.NoError(t, err)
					require.Equal(t, original, data, "removal must restore %s", name)
				}
				if !reinstall {
					current, err := os.Stat(filepath.Join(home, ".bash_profile"))
					require.NoError(t, err)
					require.True(t, os.SameFile(unmanaged, current), "an unmanaged login profile must not be rewritten")
					require.Equal(t, unmanaged.ModTime(), current.ModTime())
				}
				config, err := os.UserConfigDir()
				require.NoError(t, err)
				scripts, err := filepath.Glob(filepath.Join(config, "openai", "shell", "picker-bash-*.bash"))
				require.NoError(t, err)
				require.Empty(t, scripts, "removal must clean scripts belonging to superseded login profiles")
			}
		})
	}
}

func TestImagePickerShellSetupBashExplicitRemovalKeepsOtherProfiles(t *testing.T) {
	home := pickerShellSetupHome(t)
	profile := filepath.Join(home, ".profile")
	original := []byte("# explicit removal target\n")
	require.NoError(t, os.WriteFile(profile, original, 0600))
	_, err := runPickerShellSetup(t, t.Context(), "bash", "--install-picker")
	require.NoError(t, err)
	interactive := filepath.Join(home, ".bashrc")
	installed, err := os.ReadFile(interactive)
	require.NoError(t, err)
	_, err = runPickerShellSetup(t, t.Context(), "bash", "--uninstall-picker", "--profile", profile)
	require.NoError(t, err)
	data, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Equal(t, original, data)
	data, err = os.ReadFile(interactive)
	require.NoError(t, err)
	require.Equal(t, installed, data, "an explicit override must limit removal to that profile")
	targets, err := imagePickerShellTarget(t.Context(), "bash", false, interactive)
	require.NoError(t, err)
	kept, err := autocomplete.IsPickerInstalled(t.Context(), targets[0])
	require.NoError(t, err)
	require.True(t, kept, "the other profile's script must remain usable")
}

func TestImagePickerShellSetupPartialFailureIsRetryable(t *testing.T) {
	home := pickerShellSetupHome(t)
	interactive, login := filepath.Join(home, ".bashrc"), filepath.Join(home, ".bash_profile")
	original := []byte("# personal startup\n")
	modified := []byte("# >>> openai image picker modified\n")
	require.NoError(t, os.WriteFile(interactive, original, 0600))
	require.NoError(t, os.WriteFile(login, modified, 0600))
	output, err := runPickerShellSetup(t, t.Context(), "bash", "--install-picker")
	require.ErrorContains(t, err, "Some startup files may already be configured")
	require.Empty(t, output)
	data, err := os.ReadFile(interactive)
	require.NoError(t, err)
	require.Contains(t, string(data), "# >>> openai image picker v1 >>>")
	data, err = os.ReadFile(login)
	require.NoError(t, err)
	require.Equal(t, modified, data)
	// Simulate the user resolving the changed second profile before retrying.
	require.NoError(t, os.WriteFile(login, original, 0600))
	_, err = runPickerShellSetup(t, t.Context(), "bash", "--install-picker")
	require.NoError(t, err)
	for _, profile := range []string{interactive, login} {
		data, err := os.ReadFile(profile)
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(string(data), "# >>> openai image picker v1 >>>"))
	}
	_, err = runPickerShellSetup(t, t.Context(), "bash", "--uninstall-picker")
	require.NoError(t, err)
	for _, profile := range []string{interactive, login} {
		data, err := os.ReadFile(profile)
		require.NoError(t, err)
		require.Equal(t, original, data)
	}
}

func TestImagePickerShellSetupBashRemovalContinuesAfterUnrelatedFailure(t *testing.T) {
	for _, unsafe := range []string{"symlink", "group writable"} {
		t.Run(unsafe, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("Unix symlink and permission fixtures")
			}
			home := pickerShellSetupHome(t)
			profile := filepath.Join(home, ".profile")
			original := "# previous login settings\n"
			require.NoError(t, os.WriteFile(profile, []byte(original), 0600))
			_, err := runPickerShellSetup(t, t.Context(), "bash", "--install-picker")
			require.NoError(t, err)
			preferred := filepath.Join(home, ".bash_profile")
			personal := "# independent preferred login settings\n"
			if unsafe == "symlink" {
				target := filepath.Join(home, "user-profile")
				require.NoError(t, os.WriteFile(target, []byte(personal), 0600))
				require.NoError(t, os.Symlink(target, preferred))
			} else {
				require.NoError(t, os.WriteFile(preferred, []byte(personal), 0600))
				require.NoError(t, os.Chmod(preferred, 0660))
			}
			before, err := os.Lstat(preferred)
			require.NoError(t, err)
			modified := "# >>> openai image picker modified\n"
			require.NoError(t, os.WriteFile(filepath.Join(home, ".bash_login"), []byte(modified), 0600))
			for range 2 {
				output, err := runPickerShellSetup(t, t.Context(), "bash", "--uninstall-picker")
				require.ErrorContains(t, err, "Could not finish Tab shortcut setup")
				require.Empty(t, output, "partial cleanup must not claim success")
				data, readErr := os.ReadFile(profile)
				require.NoError(t, readErr)
				require.Equal(t, original, string(data), "an independent unsafe file must not stop intact profile cleanup")
				var failure *imageSavingError
				require.ErrorAs(t, err, &failure)
				if unsafe == "symlink" {
					require.ErrorContains(t, failure.cause, "requires a regular startup file")
				} else {
					require.ErrorContains(t, failure.cause, "regular files writable only by their owner")
				}
				require.ErrorContains(t, failure.cause, "ambiguous integration markers", "both independent failures must be retained")
				after, statErr := os.Lstat(preferred)
				require.NoError(t, statErr)
				require.True(t, os.SameFile(before, after))
				require.Equal(t, before.Mode(), after.Mode())
				_, statErr = os.Lstat(filepath.Join(home, ".bashrc"))
				require.ErrorIs(t, statErr, os.ErrNotExist)
				for path, want := range map[string]string{preferred: personal, filepath.Join(home, ".bash_login"): modified} {
					data, readErr := os.ReadFile(path)
					require.NoError(t, readErr)
					require.Equal(t, want, string(data))
				}
				config, configErr := os.UserConfigDir()
				require.NoError(t, configErr)
				scripts, globErr := filepath.Glob(filepath.Join(config, "openai", "shell", "picker-bash-*.bash"))
				require.NoError(t, globErr)
				require.Empty(t, scripts)
				declined, preferenceErr := imagePickerTabDeclined("bash")
				require.NoError(t, preferenceErr)
				require.True(t, declined, "explicit removal must stay off after partial cleanup")
			}
		})
	}
}

func TestImagePickerShellSetupRemovalRetainsPreferenceAndCleanupFailures(t *testing.T) {
	for _, modified := range []bool{false, true} {
		t.Run(fmt.Sprintf("modified=%t", modified), func(t *testing.T) {
			home := pickerShellSetupHome(t)
			profile := filepath.Join(home, ".zshrc")
			original := "# personal settings\n"
			require.NoError(t, os.WriteFile(profile, []byte(original), 0600))
			_, err := runPickerShellSetup(t, t.Context(), "zsh", "--install-picker")
			require.NoError(t, err)
			if modified {
				original = "# >>> openai image picker modified\n"
				require.NoError(t, os.WriteFile(profile, []byte(original), 0600))
			}
			preference, err := imagePickerTabChoicePath("zsh")
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(preference, 0700))
			output, err := runPickerShellSetup(t, t.Context(), "zsh", "--uninstall-picker")
			require.Error(t, err)
			require.Empty(t, output)
			var failure *imageSavingError
			require.ErrorAs(t, err, &failure)
			require.ErrorContains(t, failure.cause, "invalid Tab shortcut preference")
			if modified {
				require.ErrorContains(t, failure.cause, "ambiguous integration markers")
			}
			data, readErr := os.ReadFile(profile)
			require.NoError(t, readErr)
			require.Equal(t, original, string(data), "a preference failure must neither block cleanup nor alter a modified profile")
		})
	}
}

func TestImagePickerShellSetupRejectsInvalidActionCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"--automatic"}, {"zsh", "--profile", "startup"},
		{"zsh", "--install-picker", "--uninstall-picker"},
		{"zsh", "--install-picker", "--picker"},
		{"bash", "zsh", "--install-picker"},
		{"bash", "--install-picker", "--automatic"},
		{"--install-picker", "--automatic", "--profile", "startup"},
		{"--uninstall-picker", "--automatic"},
		{"zsh", "--install-picker", "--profile", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := pickerShellSetupHome(t)
			_, err := runPickerShellSetup(t, t.Context(), args...)
			var exit cli.ExitCoder
			require.ErrorAs(t, err, &exit)
			require.Equal(t, 2, exit.ExitCode())
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			require.Empty(t, entries, "invalid options must not persist state")
		})
	}
}

func TestImagePickerShellSetupUnknownShellAndAutomaticHomeDoNotWrite(t *testing.T) {
	for _, args := range [][]string{{"unknown", "--install-picker"}, {"--automatic", "--install-picker"}} {
		home := pickerShellSetupHome(t)
		t.Setenv("SHELL", "/bin/zsh")
		output, err := runPickerShellSetup(t, t.Context(), args...)
		require.Error(t, err)
		require.NotContains(t, output, "enabled")
		entries, err := os.ReadDir(home)
		require.NoError(t, err)
		require.Empty(t, entries)
	}
}

func TestImagePickerShellSetupCanceledBeforeActionDoesNotWrite(t *testing.T) {
	home := pickerShellSetupHome(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runPickerShellSetup(t, ctx, "zsh", "--install-picker")
	require.Error(t, err)
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestImagePickerShellSetupCancellationWhileAnotherSetupHoldsLock(t *testing.T) {
	home := pickerShellSetupHome(t)
	profile := filepath.Join(home, "startup")
	original := []byte("# preserve while waiting for another setup\n")
	require.NoError(t, os.WriteFile(profile, original, 0600))
	targets, err := imagePickerShellTarget(t.Context(), "zsh", false, profile)
	require.NoError(t, err)
	locked, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- autocomplete.WithPickerSetupLock(t.Context(), targets[0].Directory, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-done:
		close(release)
		t.Fatalf("could not hold setup lock: %v", err)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("setup lock was not acquired")
	}
	defer func() {
		close(release)
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Error("setup lock worker did not finish before fixture cleanup")
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	_, err = runPickerShellSetup(t, ctx, "zsh", "--install-picker", "--profile", profile)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	data, err := os.ReadFile(profile)
	require.NoError(t, err)
	require.Equal(t, original, data, "a blocked setup must not edit profiles before acquiring the shared lock")
	declined, err := imagePickerTabDeclined("zsh")
	require.NoError(t, err)
	require.False(t, declined)
}

func TestImagePickerShellSetupPreservesCompletionOutput(t *testing.T) {
	home := pickerShellSetupHome(t)
	normal, err := runPickerShellSetup(t, t.Context(), "zsh")
	require.NoError(t, err)
	picker, err := runPickerShellSetup(t, t.Context(), "zsh", "--picker")
	require.NoError(t, err)
	require.NotEmpty(t, normal)
	require.True(t, strings.HasPrefix(picker, normal))
	require.Greater(t, len(picker), len(normal))
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries, "emitting scripts must not install or save a choice")
}

func TestImagePickerTabChoicePrivateIdempotentAndCanceled(t *testing.T) {
	pickerShellSetupHome(t)
	path, err := imagePickerTabChoicePath("zsh")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, declineImagePickerTab(ctx, "zsh"), context.Canceled)
	_, err = os.Lstat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	for range 2 {
		require.NoError(t, declineImagePickerTab(t.Context(), "zsh"))
		declined, err := imagePickerTabDeclined("zsh")
		require.NoError(t, err)
		require.True(t, declined)
	}
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	require.Zero(t, info.Size())
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	for range 2 {
		require.NoError(t, clearImagePickerTabDecline("zsh"))
	}
	declined, err := imagePickerTabDeclined("zsh")
	require.NoError(t, err)
	require.False(t, declined)
}

func TestImagePickerTabChoiceRejectsModifiedOrSymlinkMarker(t *testing.T) {
	for _, kind := range []string{"content", "directory", "public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" && (kind == "public" || kind == "symlink") {
				t.Skip("Unix mode or symlink semantics")
			}
			home := pickerShellSetupHome(t)
			path, err := imagePickerTabChoicePath("zsh")
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			target := filepath.Join(home, "preserved-file")
			require.NoError(t, os.WriteFile(target, []byte("preserve these bytes"), 0600))
			switch kind {
			case "content":
				require.NoError(t, os.WriteFile(path, []byte("external content"), 0600))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0700))
			case "public":
				require.NoError(t, os.WriteFile(path, nil, 0600))
				require.NoError(t, os.Chmod(path, 0644))
			case "symlink":
				require.NoError(t, os.Symlink(target, path))
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			_, err = imagePickerTabDeclined("zsh")
			require.Error(t, err)
			require.Error(t, declineImagePickerTab(t.Context(), "zsh"))
			require.Error(t, clearImagePickerTabDecline("zsh"))
			after, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "preserve these bytes", string(data))
		})
	}
}
