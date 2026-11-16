package autocomplete

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func suggestionInstallFixture(t *testing.T, shell CompletionStyle) SuggestionInstallation {
	t.Helper()
	base := pickerInstallFixture(t, shell)
	return SuggestionInstallation{
		Shell: shell, Directory: base.Directory, Profile: base.Profile,
		Executable: filepath.Join(filepath.Dir(base.Profile), "bin with ' quotes", "openai"),
	}
}

func TestSuggestionInstallRoundTripAndSavedSettings(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			options := suggestionInstallFixture(t, shell)
			original := []byte("# personal startup without final newline")
			require.NoError(t, os.WriteFile(options.Profile, original, 0640))
			installed, err := IsSuggestionsInstalled(t.Context(), options)
			require.NoError(t, err)
			require.False(t, installed)
			first, err := InstallSuggestions(t.Context(), options)
			require.NoError(t, err)
			require.True(t, first.Changed)
			require.Contains(t, filepath.Base(first.ScriptPath), "suggestions-"+string(shell)+"-")
			content, err := os.ReadFile(first.ScriptPath)
			require.NoError(t, err)
			require.Contains(t, string(content), "@completion "+string(shell)+" --suggestions")
			require.NotContains(t, string(content), "suggestions-double-tab")
			require.NotContains(t, string(content), "suggestions-click")
			require.NotContains(t, string(content), "__EXECUTABLE__")
			require.NotContains(t, string(content), "__DOUBLE_TAB_ARGUMENT__")
			profile, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(profile, original))
			require.NotContains(t, string(profile), "openai image picker")
			if runtime.GOOS != "windows" {
				info, err := os.Stat(options.Profile)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0640), info.Mode().Perm())
			}
			again, err := InstallSuggestions(t.Context(), options)
			require.NoError(t, err)
			require.False(t, again.Changed)
			query := options
			query.Executable = ""
			status, err := InspectSuggestions(t.Context(), query)
			require.NoError(t, err)
			require.Equal(t, SuggestionStatus{Installed: true, Executable: options.Executable, ScriptPath: first.ScriptPath}, status)
			options.DoubleTab = true
			updated, err := InstallSuggestions(t.Context(), options)
			require.NoError(t, err)
			require.True(t, updated.Changed)
			require.NotEqual(t, first.ScriptPath, updated.ScriptPath)
			_, err = os.Stat(first.ScriptPath)
			require.ErrorIs(t, err, os.ErrNotExist)
			status, err = InspectSuggestions(t.Context(), query)
			require.NoError(t, err)
			require.True(t, status.DoubleTab)
			removed, err := RemoveSuggestions(t.Context(), query)
			require.NoError(t, err)
			require.True(t, removed.Changed)
			restored, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, original, restored)
			removed, err = RemoveSuggestions(t.Context(), query)
			require.NoError(t, err)
			require.False(t, removed.Changed)
		})
	}
}

func TestSuggestionInstallCoexistsWithPicker(t *testing.T) {
	for _, existingProfile := range []bool{false, true} {
		for _, suggestionsFirst := range []bool{false, true} {
			for _, removeSuggestionsFirst := range []bool{false, true} {
				t.Run(strings.Join([]string{boolName(existingProfile), boolName(suggestionsFirst), boolName(removeSuggestionsFirst)}, "/"), func(t *testing.T) {
					options := suggestionInstallFixture(t, CompletionStyleZsh)
					picker := PickerInstallation{Shell: options.Shell, Directory: options.Directory, Profile: options.Profile}
					original := []byte("# personal startup\n")
					if existingProfile {
						require.NoError(t, os.WriteFile(options.Profile, original, 0600))
					}
					addSuggestions := func() { _, err := InstallSuggestions(t.Context(), options); require.NoError(t, err) }
					addPicker := func() { _, err := InstallPicker(t.Context(), picker); require.NoError(t, err) }
					if suggestionsFirst {
						addSuggestions()
						addPicker()
					} else {
						addPicker()
						addSuggestions()
					}
					profile, err := os.ReadFile(options.Profile)
					require.NoError(t, err)
					require.Equal(t, 1, bytes.Count(profile, []byte("# >>> openai command insertion")))
					require.Equal(t, 1, bytes.Count(profile, []byte(pickerBlockBegin)))
					if removeSuggestionsFirst {
						_, err = RemoveSuggestions(t.Context(), options)
						require.NoError(t, err)
						intact, err := IsPickerInstalled(t.Context(), picker)
						require.NoError(t, err)
						require.True(t, intact)
						_, err = RemovePicker(t.Context(), picker)
						require.NoError(t, err)
					} else {
						_, err = RemovePicker(t.Context(), picker)
						require.NoError(t, err)
						intact, err := IsSuggestionsInstalled(t.Context(), options)
						require.NoError(t, err)
						require.True(t, intact)
						_, err = RemoveSuggestions(t.Context(), options)
						require.NoError(t, err)
					}
					after, err := os.ReadFile(options.Profile)
					if existingProfile {
						require.NoError(t, err)
						require.Equal(t, original, after)
					} else {
						require.ErrorIs(t, err, os.ErrNotExist)
					}
				})
			}
		}
	}
}

func boolName(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func TestSuggestionInstallConcurrentWithPicker(t *testing.T) {
	options := suggestionInstallFixture(t, CompletionStyleBash)
	picker := PickerInstallation{Shell: options.Shell, Directory: options.Directory, Profile: options.Profile}
	var group sync.WaitGroup
	errors := make(chan error, 2)
	for _, install := range []func() error{
		func() error { _, err := InstallSuggestions(t.Context(), options); return err },
		func() error { _, err := InstallPicker(t.Context(), picker); return err },
	} {
		group.Go(func() { errors <- install() })
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	pickerPresent, err := IsPickerInstalled(t.Context(), picker)
	require.NoError(t, err)
	require.True(t, pickerPresent)
	suggestionsPresent, err := IsSuggestionsInstalled(t.Context(), options)
	require.NoError(t, err)
	require.True(t, suggestionsPresent)
}

func TestSuggestionInstallValidationAndReadOnlyStatus(t *testing.T) {
	options := suggestionInstallFixture(t, CompletionStyleZsh)
	for _, path := range []string{"", "relative", options.Executable + "/.", options.Executable + "\n", options.Executable + "\t"} {
		invalid := options
		invalid.Executable = path
		_, err := InstallSuggestions(t.Context(), invalid)
		require.Error(t, err)
	}
	_, err := os.Stat(options.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
	status, err := InspectSuggestions(t.Context(), options)
	require.NoError(t, err)
	require.False(t, status.Installed)
	_, err = os.Stat(options.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = InstallSuggestions(ctx, options)
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(options.Profile)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSuggestionInstallPreservesModifiedState(t *testing.T) {
	for _, changed := range []string{"script", "profile", "missing-script"} {
		t.Run(changed, func(t *testing.T) {
			options := suggestionInstallFixture(t, CompletionStyleZsh)
			result, err := InstallSuggestions(t.Context(), options)
			require.NoError(t, err)
			if changed == "script" {
				require.NoError(t, os.WriteFile(result.ScriptPath, []byte("# changed manually\n"), 0600))
			} else if changed == "profile" {
				content, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(options.Profile, bytes.Replace(content, []byte("# openai-suggestions: "), []byte("# altered: "), 1), 0600))
			} else {
				require.NoError(t, os.Remove(result.ScriptPath))
				status, err := InspectSuggestions(t.Context(), options)
				require.NoError(t, err)
				require.False(t, status.Installed)
				_, err = RemoveSuggestions(t.Context(), options)
				require.NoError(t, err)
				return
			}
			before, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			_, err = InspectSuggestions(t.Context(), options)
			require.Error(t, err)
			_, err = InstallSuggestions(t.Context(), options)
			require.Error(t, err)
			_, err = RemoveSuggestions(t.Context(), options)
			require.Error(t, err)
			after, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestSuggestionInstallPreservesTemplateTokensInExecutablePath(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, doubleTab := range []bool{false, true} {
			options := suggestionInstallFixture(t, shell)
			options.Executable = filepath.Join(filepath.Dir(options.Profile), "__EXECUTABLE__ '__DOUBLE_TAB_ARGUMENT__", "openai")
			options.DoubleTab = doubleTab
			loader, err := renderInstalledSuggestions(options.installation())
			require.NoError(t, err)
			require.Contains(t, string(loader), quotePickerPath(shell, options.Executable))
		}
	}
}
