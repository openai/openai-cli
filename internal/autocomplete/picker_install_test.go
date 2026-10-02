package autocomplete

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pickerInstallFixture(t *testing.T, shell CompletionStyle) PickerInstallation {
	t.Helper()
	home := t.TempDir()
	return PickerInstallation{Shell: shell, Directory: filepath.Join(home, "settings with ' quotes", "shell"), Profile: filepath.Join(home, "profile")}
}

func TestPickerInstallRoundTrip(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, original := range []string{"", "# personal setting", "# personal setting\n", "\xef\xbb\xbf# Windows setting\r\n"} {
			t.Run(string(shell)+"/"+original, func(t *testing.T) {
				options := pickerInstallFixture(t, shell)
				require.NoError(t, os.WriteFile(options.Profile, []byte(original), 0640))
				installed, err := IsPickerInstalled(context.Background(), options)
				require.NoError(t, err)
				require.False(t, installed)
				first, err := InstallPicker(context.Background(), options)
				require.NoError(t, err)
				require.True(t, first.Changed)
				profile, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.True(t, bytes.HasPrefix(profile, []byte(original)))
				require.Equal(t, 1, bytes.Count(profile, []byte(pickerBlockBegin)))
				info, err := os.Stat(options.Profile)
				require.NoError(t, err)
				if runtime.GOOS != "windows" {
					require.Equal(t, os.FileMode(0640), info.Mode().Perm())
				}
				installed, err = IsPickerInstalled(context.Background(), options)
				require.NoError(t, err)
				require.True(t, installed)
				second, err := InstallPicker(context.Background(), options)
				require.NoError(t, err)
				require.False(t, second.Changed)
				require.Equal(t, first.ScriptPath, second.ScriptPath)
				// Content after the owned block also survives removal.
				require.NoError(t, os.WriteFile(options.Profile, append(profile, []byte("# added afterwards\n")...), 0640))
				removed, err := RemovePicker(context.Background(), options)
				require.NoError(t, err)
				require.True(t, removed.Changed)
				restored, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.Equal(t, original+"# added afterwards\n", string(restored))
				_, err = os.Stat(first.ScriptPath)
				require.ErrorIs(t, err, os.ErrNotExist)
				third, err := RemovePicker(context.Background(), options)
				require.NoError(t, err)
				require.False(t, third.Changed)
			})
		}
	}
}

func TestPickerInstallRejectsPowerShellWithoutWriting(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStylePowershell)
	before, err := os.Stat(filepath.Dir(options.Profile))
	require.NoError(t, err)
	for _, operation := range []func(context.Context, PickerInstallation) (PickerInstallResult, error){InstallPicker, RemovePicker} {
		_, err = operation(t.Context(), options)
		require.ErrorContains(t, err, "PowerShell uses normal Tab completion")
	}
	_, err = IsPickerInstalled(t.Context(), options)
	require.ErrorContains(t, err, "PowerShell uses normal Tab completion")
	entries, err := os.ReadDir(filepath.Dir(options.Profile))
	require.NoError(t, err)
	require.Empty(t, entries)
	after, err := os.Stat(filepath.Dir(options.Profile))
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime())
}

func TestPickerInstallPreservesModifiedFiles(t *testing.T) {
	for _, scenario := range []string{"script", "block", "duplicate", "future version"} {
		t.Run(scenario, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleZsh)
			result, err := InstallPicker(context.Background(), options)
			require.NoError(t, err)
			profile, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			switch scenario {
			case "script":
				require.NoError(t, os.WriteFile(result.ScriptPath, []byte("# my replacement\n"), 0600))
			case "block":
				profile = bytes.ReplaceAll(profile, []byte("  source "), []byte("  . "))
			case "duplicate":
				profile = append(profile, profile...)
			case "future version":
				profile = bytes.ReplaceAll(profile, []byte("v1 >>>"), []byte("v2 >>>"))
			}
			require.NoError(t, os.WriteFile(options.Profile, profile, 0600))
			for _, operation := range []func(context.Context, PickerInstallation) (PickerInstallResult, error){InstallPicker, RemovePicker} {
				_, err := operation(context.Background(), options)
				require.Error(t, err)
				actual, readErr := os.ReadFile(options.Profile)
				require.NoError(t, readErr)
				require.Equal(t, profile, actual)
			}
		})
	}
}

func TestPickerInstallRefusesUnsafeAndLargeFiles(t *testing.T) {
	for _, scenario := range []string{"symlink profile", "directory profile", "symlink directory", "large", "NUL", "invalid UTF-8", "world writable"} {
		t.Run(scenario, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			switch scenario {
			case "symlink profile":
				target := filepath.Join(filepath.Dir(options.Profile), "target")
				require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0600))
				if err := os.Symlink(target, options.Profile); err != nil {
					t.Skip(err)
				}
			case "directory profile":
				require.NoError(t, os.Mkdir(options.Profile, 0700))
			case "symlink directory":
				require.NoError(t, os.MkdirAll(filepath.Dir(options.Directory), 0700))
				if err := os.Symlink(t.TempDir(), options.Directory); err != nil {
					t.Skip(err)
				}
			case "large":
				require.NoError(t, os.WriteFile(options.Profile, bytes.Repeat([]byte{'#'}, pickerInstallLimit+1), 0600))
			case "NUL":
				require.NoError(t, os.WriteFile(options.Profile, []byte("x\x00x"), 0600))
			case "invalid UTF-8":
				require.NoError(t, os.WriteFile(options.Profile, []byte{0xff}, 0600))
			case "world writable":
				if runtime.GOOS == "windows" {
					t.Skip("Unix permission model")
				}
				require.NoError(t, os.WriteFile(options.Profile, nil, 0600))
				require.NoError(t, os.Chmod(options.Profile, 0666))
			}
			_, err := InstallPicker(context.Background(), options)
			require.Error(t, err)
		})
	}
}

func TestPickerInstallConcurrentAndSeparateProfiles(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	var wg sync.WaitGroup
	errorsFound := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := InstallPicker(context.Background(), options); errorsFound <- err }()
	}
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
	profile, err := os.ReadFile(options.Profile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(profile, []byte(pickerBlockBegin)))
	first, err := InstallPicker(context.Background(), options)
	require.NoError(t, err)
	other := options
	other.Profile += ".login"
	second, err := InstallPicker(context.Background(), other)
	require.NoError(t, err)
	require.NotEqual(t, first.ScriptPath, second.ScriptPath)
	_, err = RemovePicker(context.Background(), options)
	require.NoError(t, err)
	installed, err := IsPickerInstalled(context.Background(), other)
	require.NoError(t, err)
	require.True(t, installed)
}

func TestPickerInstallCancellationAndReplacedLock(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleZsh)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := InstallPicker(ctx, options)
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(options.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
	root, err := openPickerDirectory(filepath.Dir(options.Profile), false)
	require.NoError(t, err)
	defer root.Close()
	name := "." + filepath.Base(options.Profile) + ".openai-picker.lock"
	lock, err := lockPickerInstallation(context.Background(), root, name)
	require.NoError(t, err)
	defer lock.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = InstallPicker(ctx, options)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = os.Stat(options.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPickerInstallSnapshotDetectsReplacementAndRewrite(t *testing.T) {
	for _, replace := range []bool{true, false} {
		t.Run(map[bool]string{true: "replace", false: "rewrite"}[replace], func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleZsh)
			require.NoError(t, os.WriteFile(options.Profile, []byte("before"), 0600))
			root, err := openPickerDirectory(filepath.Dir(options.Profile), false)
			require.NoError(t, err)
			defer root.Close()
			before, err := readPickerFile(context.Background(), root, filepath.Base(options.Profile))
			require.NoError(t, err)
			if replace {
				require.NoError(t, os.Remove(options.Profile))
			}
			require.NoError(t, os.WriteFile(options.Profile, []byte("user's external update"), 0600))
			err = replacePickerProfile(context.Background(), root, filepath.Base(options.Profile), before, []byte("would overwrite update"))
			require.ErrorIs(t, err, errPickerInstallChanged)
			actual, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, "user's external update", string(actual))
		})
	}
}

func TestPickerInstallRollbackAndMissingScriptRemoval(t *testing.T) {
	t.Run("profile failure", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs unprivileged Unix permissions")
		}
		options := pickerInstallFixture(t, CompletionStyleZsh)
		profileDir := filepath.Join(filepath.Dir(options.Profile), "startup")
		require.NoError(t, os.Mkdir(profileDir, 0700))
		options.Profile = filepath.Join(profileDir, "profile")
		require.NoError(t, os.WriteFile(options.Profile, []byte("# preserve me\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(profileDir, ".profile.openai-picker.lock"), nil, 0600))
		require.NoError(t, os.Chmod(profileDir, 0500))
		defer os.Chmod(profileDir, 0700)
		_, err := InstallPicker(context.Background(), options)
		require.Error(t, err)
		files, err := os.ReadDir(options.Directory)
		require.NoError(t, err)
		for _, file := range files {
			require.Equal(t, ".picker-install.lock", file.Name())
		}
		profile, err := os.ReadFile(options.Profile)
		require.NoError(t, err)
		require.Equal(t, "# preserve me\n", string(profile))
	})
	for _, missingDirectory := range []bool{false, true} {
		t.Run(map[bool]string{true: "missing directory", false: "missing script"}[missingDirectory], func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleZsh)
			result, err := InstallPicker(context.Background(), options)
			require.NoError(t, err)
			if missingDirectory {
				require.NoError(t, os.RemoveAll(options.Directory))
			} else {
				require.NoError(t, os.Remove(result.ScriptPath))
			}
			_, err = RemovePicker(context.Background(), options)
			require.NoError(t, err)
			actual, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Empty(t, actual)
		})
	}
}

func TestPickerInstallScriptPublicationIsExclusiveAndCancellable(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	root, err := openPickerDirectory(options.Directory, true)
	require.NoError(t, err)
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = writePickerScript(ctx, root, "script", []byte("owned"))
	require.ErrorIs(t, err, context.Canceled)
	files, err := os.ReadDir(options.Directory)
	require.NoError(t, err)
	require.Empty(t, files)
	require.NoError(t, os.WriteFile(filepath.Join(options.Directory, "script"), []byte("foreign"), 0600))
	_, err = writePickerScript(context.Background(), root, "script", []byte("owned"))
	require.True(t, errors.Is(err, os.ErrExist), err)
	actual, err := os.ReadFile(filepath.Join(options.Directory, "script"))
	require.NoError(t, err)
	require.Equal(t, "foreign", string(actual))
	files, err = os.ReadDir(options.Directory)
	require.NoError(t, err)
	require.Len(t, files, 1)
}

func TestPickerInstallKeepsRuntimeCommandAndGuardedMarker(t *testing.T) {
	markers := map[CompletionStyle]string{
		CompletionStyleBash: "export OPENAI_PICKER_INTEGRATION=bash",
		CompletionStyleZsh:  "export OPENAI_PICKER_INTEGRATION=zsh",
		CompletionStyleFish: "set -gx OPENAI_PICKER_INTEGRATION fish",
	}
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			options := pickerInstallFixture(t, shell)
			data, err := renderInstalledPicker(options)
			require.NoError(t, err)
			require.Contains(t, string(data), markers[shell])
			require.NotContains(t, string(data), options.Directory)
			require.False(t, strings.Contains(string(data), "__APPNAME__"))
		})
	}
}

// Observing the first lock-loop context check gives a deterministic handshake:
// the waiting writer has already opened and identified the original lock.
type pickerInstallNotifyContext struct {
	context.Context
	once   sync.Once
	opened chan struct{}
}

func (ctx *pickerInstallNotifyContext) Err() error {
	ctx.once.Do(func() { close(ctx.opened) })
	return ctx.Context.Err()
}

func TestPickerInstallRejectsReplacedWaitingLock(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleZsh)
	root, err := openPickerDirectory(options.Directory, true)
	require.NoError(t, err)
	defer root.Close()
	first, err := lockPickerInstallation(context.Background(), root, ".lock")
	require.NoError(t, err)
	defer first.Close()
	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx := &pickerInstallNotifyContext{Context: deadline, opened: make(chan struct{})}
	finished := make(chan error, 1)
	go func() {
		lock, err := lockPickerInstallation(ctx, root, ".lock")
		if lock != nil {
			lock.Close()
		}
		finished <- err
	}()
	<-ctx.opened
	require.NoError(t, root.Rename(".lock", ".old-lock"))
	require.NoError(t, os.WriteFile(filepath.Join(options.Directory, ".lock"), nil, 0600))
	require.NoError(t, first.Close())
	require.ErrorIs(t, <-finished, errPickerInstallChanged)
}

// Cancel at the observable transaction boundary, after publishing the complete
// script and before replacing the user's startup file.
type pickerInstallCancelAfterScript struct {
	context.Context
	script string
}

func (ctx pickerInstallCancelAfterScript) Err() error {
	if _, err := os.Stat(ctx.script); err == nil {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestPickerInstallCancelsBetweenScriptAndProfile(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	require.NoError(t, os.WriteFile(options.Profile, []byte("# keep my startup\n"), 0600))
	data, err := renderInstalledPicker(options)
	require.NoError(t, err)
	ctx := pickerInstallCancelAfterScript{context.Background(), filepath.Join(options.Directory, pickerScriptName(options, data))}
	_, err = InstallPicker(ctx, options)
	require.ErrorIs(t, err, context.Canceled)
	profile, err := os.ReadFile(options.Profile)
	require.NoError(t, err)
	require.Equal(t, "# keep my startup\n", string(profile))
	files, err := os.ReadDir(options.Directory)
	require.NoError(t, err)
	for _, file := range files {
		require.Equal(t, ".picker-install.lock", file.Name())
	}
}

func TestPickerInstallWholeSetupLockCoordinatesProfilesAndCancellation(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- WithPickerSetupLock(context.Background(), options.Directory, func() error {
			_, err := InstallPicker(context.Background(), options)
			close(entered)
			<-release
			return err
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := WithPickerSetupLock(ctx, options.Directory, func() error {
		return errors.New("unexpected callback during another setup")
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	require.NoError(t, <-finished)
	err = WithPickerSetupLock(context.Background(), options.Directory, func() error {
		_, err := RemovePicker(context.Background(), options)
		return err
	})
	require.NoError(t, err)
}
