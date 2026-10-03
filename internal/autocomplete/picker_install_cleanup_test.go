package autocomplete

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
