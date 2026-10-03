package autocomplete

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type pickerCancelAfterProfileRemoval struct {
	context.Context
	profile string
}

func (ctx pickerCancelAfterProfileRemoval) Err() error {
	data, err := os.ReadFile(ctx.profile)
	if errors.Is(err, os.ErrNotExist) || err == nil && !bytes.Contains(data, []byte(pickerBlockBegin)) {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestPickerRemoveRetriesScriptCleanup(t *testing.T) {
	for _, created := range []bool{false, true} {
		t.Run(fmt.Sprintf("created=%t", created), func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			personal := []byte("# personal startup\n")
			if !created {
				require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
			}
			installed, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			peer := options
			peer.Profile += "-peer"
			peerInstall, err := InstallPicker(t.Context(), peer)
			require.NoError(t, err)
			// Recover the exact namespace used by the opened profile identity.
			namespace := filepath.Base(installed.ScriptPath)[:len("picker-bash-")+16+1]
			obsolete := []byte(pickerScriptHeader + "# older owned script\n")
			obsoleteName := namespace + pickerScriptName(options, obsolete)[len(namespace):]
			obsoletePath := filepath.Join(options.Directory, obsoleteName)
			require.NoError(t, os.WriteFile(obsoletePath, obsolete, 0600))
			modified := []byte(pickerScriptHeader + "# original bytes\n")
			modifiedName := namespace + pickerScriptName(options, modified)[len(namespace):]
			modifiedPath := filepath.Join(options.Directory, modifiedName)
			require.NoError(t, os.WriteFile(modifiedPath, []byte("# personal modification\n"), 0600))
			removed, err := RemovePicker(pickerCancelAfterProfileRemoval{t.Context(), options.Profile}, options)
			require.ErrorIs(t, err, context.Canceled)
			require.True(t, removed.Changed)
			_, err = os.Stat(installed.ScriptPath)
			require.NoError(t, err, "cleanup canceled after the profile commit leaves a retryable script")
			before, statErr := os.Stat(options.Profile)
			if created {
				require.ErrorIs(t, statErr, os.ErrNotExist)
			} else {
				require.NoError(t, statErr)
			}
			retried, err := RemovePicker(t.Context(), options)
			require.NoError(t, err)
			require.True(t, retried.Changed)
			for _, path := range []string{installed.ScriptPath, obsoletePath} {
				_, err = os.Stat(path)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			for _, path := range []string{peerInstall.ScriptPath, modifiedPath} {
				_, err = os.Stat(path)
				require.NoError(t, err, "unproven scripts must survive cleanup retry")
			}
			after, statErr := os.Stat(options.Profile)
			if created {
				require.ErrorIs(t, statErr, os.ErrNotExist)
			} else {
				require.NoError(t, statErr)
				require.True(t, os.SameFile(before, after), "retry must not rewrite the restored profile")
				data, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.Equal(t, personal, data)
			}
			again, err := RemovePicker(t.Context(), options)
			require.NoError(t, err)
			require.False(t, again.Changed)
			active, err := IsPickerInstalled(t.Context(), peer)
			require.NoError(t, err)
			require.True(t, active)
		})
	}
}

// The profile rename is observable before the next context check in cleanup.
type pickerCancelAfterProfileCommit struct {
	context.Context
	profile string
	script  string
}

func (ctx pickerCancelAfterProfileCommit) Err() error {
	data, _ := os.ReadFile(ctx.profile)
	if bytes.Contains(data, []byte(filepath.Base(ctx.script))) {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestPickerInstallRetriesSupersededScriptCleanup(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	first, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	for round := range 3 {
		canonical := options
		canonical.Profile, err = filepath.EvalSymlinks(options.Profile)
		require.NoError(t, err)
		older := []byte(pickerScriptHeader + "# previous script " + string(rune('a'+round)) + "\n")
		oldPath := filepath.Join(options.Directory, pickerScriptName(canonical, older))
		require.NoError(t, os.WriteFile(oldPath, older, 0600))
		require.NoError(t, os.WriteFile(options.Profile, renderPickerBlock(pickerInstalledBlock{Shell: options.Shell, Script: oldPath}), 0600))
		require.NoError(t, os.Remove(first.ScriptPath))
		ctx := pickerCancelAfterProfileCommit{t.Context(), options.Profile, first.ScriptPath}
		updated, err := InstallPicker(ctx, options)
		require.ErrorIs(t, err, context.Canceled)
		require.True(t, updated.Changed)
		_, err = os.Stat(oldPath)
		require.NoError(t, err, "canceled cleanup leaves the obsolete script for retry")
		active, err := IsPickerInstalled(t.Context(), options)
		require.NoError(t, err)
		require.False(t, active, "first-run setup must notice pending cleanup despite current script bytes")
		profileBefore, err := os.Stat(options.Profile)
		require.NoError(t, err)
		retried, err := InstallPicker(t.Context(), options)
		require.NoError(t, err)
		require.True(t, retried.Changed)
		_, err = os.Stat(oldPath)
		require.ErrorIs(t, err, os.ErrNotExist)
		profileAfter, err := os.Stat(options.Profile)
		require.NoError(t, err)
		require.True(t, os.SameFile(profileBefore, profileAfter), "cleanup retry must not rewrite an intact profile")
		active, err = IsPickerInstalled(t.Context(), options)
		require.NoError(t, err)
		require.True(t, active)
		first = retried
	}
}

func TestPickerRemoveMissingProfileParentCleansOwnedScripts(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			options := pickerInstallFixture(t, shell)
			home := filepath.Dir(options.Profile)
			options.Profile = filepath.Join(home, "startup", "profile")
			installed, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			peer := options
			peer.Profile = filepath.Join(home, "peer-profile")
			peerInstalled, err := InstallPicker(t.Context(), peer)
			require.NoError(t, err)
			prefixLength := len("picker-"+string(shell)+"-") + 16 + 1
			namespace := filepath.Base(installed.ScriptPath)[:prefixLength]
			older := []byte(pickerScriptHeader + "# older owned script\n")
			oldPath := filepath.Join(options.Directory, namespace+pickerScriptName(options, older)[prefixLength:])
			require.NoError(t, os.WriteFile(oldPath, older, 0600))
			modified := []byte(pickerScriptHeader + "# original bytes\n")
			modifiedPath := filepath.Join(options.Directory, namespace+pickerScriptName(options, modified)[prefixLength:])
			require.NoError(t, os.WriteFile(modifiedPath, []byte("# personal modification\n"), 0600))
			unproven := options
			unproven.Profile += "-unknown-alias"
			unprovenPath := filepath.Join(options.Directory, pickerScriptName(unproven, older))
			require.NoError(t, os.WriteFile(unprovenPath, older, 0600))
			preserved := make(map[string][]byte)
			for _, path := range []string{peerInstalled.ScriptPath, modifiedPath, unprovenPath} {
				preserved[path], err = os.ReadFile(path)
				require.NoError(t, err)
			}
			require.NoError(t, os.RemoveAll(filepath.Dir(options.Profile)))
			removed, err := RemovePicker(t.Context(), options)
			require.NoError(t, err)
			require.True(t, removed.Changed)
			for _, path := range []string{installed.ScriptPath, oldPath, filepath.Dir(options.Profile)} {
				_, err = os.Lstat(path)
				require.ErrorIs(t, err, os.ErrNotExist, "cleanup must not recreate the missing startup directory")
			}
			for path, before := range preserved {
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, before, after, "preserve scripts outside provable ownership")
			}
			active, err := IsPickerInstalled(t.Context(), peer)
			require.NoError(t, err)
			require.True(t, active)
			again, err := RemovePicker(t.Context(), options)
			require.NoError(t, err)
			require.False(t, again.Changed)
		})
	}
}

func TestPickerRemoveMissingProfileAndScriptDirectoriesNoOp(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			options := pickerInstallFixture(t, shell)
			home := filepath.Dir(options.Profile)
			options.Profile = filepath.Join(home, "absent", "profile")
			before := pickerConfigTree(t, home)
			for range 2 {
				removed, err := RemovePicker(t.Context(), options)
				require.NoError(t, err)
				require.False(t, removed.Changed)
			}
			require.Equal(t, before, pickerConfigTree(t, home))
		})
	}
}

// Done is evaluated by the lock wait only after an acquisition failed. It
// provides a handshake without depending on the number of validation checks.
type pickerCleanupWaitingContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (ctx *pickerCleanupWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestPickerRemoveMissingProfileParentReappearsWhileWaiting(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			options := pickerInstallFixture(t, shell)
			options.Profile = filepath.Join(filepath.Dir(options.Profile), "startup", "profile")
			installed, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			profile, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			script, err := os.ReadFile(installed.ScriptPath)
			require.NoError(t, err)
			require.NoError(t, os.RemoveAll(filepath.Dir(options.Profile)))
			root, err := openPickerScriptDirectory(t.Context(), options.Directory, false)
			require.NoError(t, err)
			defer root.Close()
			lock, err := lockPickerInstallation(t.Context(), root, ".picker-install.lock")
			require.NoError(t, err)
			defer lock.Close()
			deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			ctx := &pickerCleanupWaitingContext{Context: deadline, waiting: make(chan struct{})}
			finished := make(chan error, 1)
			go func() {
				_, err := RemovePicker(ctx, options)
				finished <- err
			}()
			select {
			case <-ctx.waiting:
			case err := <-finished:
				t.Fatalf("removal returned before waiting for the script lock: %v", err)
			case <-deadline.Done():
				t.Fatal("removal did not reach the script lock")
			}
			require.NoError(t, os.Mkdir(filepath.Dir(options.Profile), 0700))
			require.NoError(t, os.WriteFile(options.Profile, profile, 0600))
			require.NoError(t, lock.Close())
			require.ErrorIs(t, <-finished, errPickerInstallChanged)
			after, err := os.ReadFile(installed.ScriptPath)
			require.NoError(t, err)
			require.Equal(t, script, after)
			after, err = os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, profile, after)
			active, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.True(t, active)
		})
	}
}
