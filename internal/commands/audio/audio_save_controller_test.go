package audio

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func audioSaveControllerResult(t *testing.T, m *audioWorkbench, data string) *audioWorkResult {
	t.Helper()
	settings := audioDefaultSettings()
	settings.Input = "Original retained prompt"
	entry, err := m.store.Capture(t.Context(), io.NopCloser(strings.NewReader(data)), settings)
	require.NoError(t, err)
	result := &audioWorkResult{entry: entry, position: 400 * time.Millisecond}
	m.result, m.focus = result, 4
	return result
}

func audioSaveControllerRun(t *testing.T, command tea.Cmd) audioWorkMessage {
	t.Helper()
	require.NotNil(t, command)
	batch, ok := command().(tea.BatchMsg)
	require.True(t, ok)
	result, ok := batch[0]().(audioWorkMessage)
	require.True(t, ok)
	return result
}

func TestAudioSaveControllerFocusAndFolderCommit(t *testing.T) {
	home := audioSaveTestHome(t)
	m := audioReviewModel(t)
	result := audioReviewResult("retained", false)
	m.result, m.focus = &result, 4
	m.openSave()
	filename := m.path.Value()
	require.Equal(t, filepath.Base(filename), filename)
	require.Equal(t, audioDefaultSaveFolder, m.saveFolder.Value())
	require.Equal(t, 2, m.saveFocus, "Save should open ready to confirm")
	for _, focus := range []int{0, 1, 2} {
		audioReviewKey(m, tea.KeyTab)
		require.Equal(t, focus, m.saveFocus)
		require.Equal(t, focus == 0, m.path.Focused())
		require.Equal(t, focus == 1, m.saveFolder.Focused())
		require.False(t, m.input.Focused())
		require.False(t, m.busy)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, 1, m.saveFocus)
	audioReviewKey(m, tea.KeyTab)
	m.Update(tea.PasteMsg{Content: "ignored by Save action"})
	require.Equal(t, filename, m.path.Value())
	audioReviewKey(m, tea.KeyUp)
	require.Equal(t, 1, m.saveFocus)
	m.saveFolder.SetValue(filepath.Join(home, "custom"))
	audioReviewKey(m, tea.KeyEnter)
	require.Equal(t, 2, m.saveFocus)
	require.Equal(t, "save", m.modal)
	require.False(t, m.busy, "Folder Enter must only finish editing")
	audioReviewKey(m, tea.KeyEscape)
	require.Empty(t, m.modal)
	require.False(t, m.path.Focused())
	require.False(t, m.saveFolder.Focused())
	_, err := os.Stat(filepath.Join(home, "Downloads"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(home, "custom"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestAudioSaveControllerRestoresFolderFocusAndWrappedNavigation(t *testing.T) {
	audioSaveTestHome(t)
	m := audioReviewModel(t)
	result := audioReviewResult("retained", false)
	m.result = &result
	m.openSave()
	m.focusSave(0)
	m.path.SetValue(strings.Repeat("x", m.path.Width()*2+1))
	m.path.CursorStart()
	audioReviewKey(m, tea.KeyDown)
	require.Equal(t, 0, m.saveFocus, "Down must first move inside a wrapped filename")
	m.path.CursorEnd()
	audioReviewKey(m, tea.KeyDown)
	require.Equal(t, 1, m.saveFocus)
	m.modal, m.infoParent = "info", "save"
	m.saveFolder.Blur()
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, "save", m.modal)
	require.True(t, m.saveFolder.Focused())
	require.False(t, m.path.Focused())
}

func TestAudioSaveControllerEditedDefaultNameCapturesFieldsAndOriginal(t *testing.T) {
	home := audioSaveTestHome(t)
	m := audioReviewModel(t)
	const original = "Complete synthetic audio from byte zero through the final byte."
	result := audioSaveControllerResult(t, m, original)
	m.input.SetValue("Edited prompt must not rename retained audio")
	for sequence, name := range []string{"renamed.wav", "renamed-2.wav"} {
		m.result = result
		m.openSave()
		require.Equal(t, "original-retained-prompt.wav", m.path.Value())
		m.path.SetValue("renamed.wav")
		command := audioReviewKey(m, tea.KeyEnter)
		require.True(t, m.busy)
		// Worker input must not follow later UI edits or result replacement.
		m.path.SetValue("later.wav")
		m.saveFolder.SetValue(filepath.Join(t.TempDir(), "missing"))
		m.result = &audioWorkResult{entry: audiohistory.Entry{ID: "later"}}
		message := audioSaveControllerRun(t, command)
		require.NoError(t, message.err)
		require.Equal(t, result.entry.ID, message.entryID)
		path := filepath.Join(home, "Downloads", "gpt-audio", name)
		require.Equal(t, path, message.path)
		m.Update(message)
		require.Empty(t, m.result.savedPaths)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, original, string(data))
		if sequence == 1 {
			first, err := os.ReadFile(filepath.Join(filepath.Dir(path), "renamed.wav"))
			require.NoError(t, err)
			require.Equal(t, original, string(first))
		}
	}
}

func TestAudioSaveControllerCustomFolderAndFullPathKeepExplicitOverwrite(t *testing.T) {
	for _, pastedPath := range []bool{false, true} {
		t.Run(map[bool]string{false: "folder", true: "full path"}[pastedPath], func(t *testing.T) {
			audioSaveTestHome(t)
			m := audioReviewModel(t)
			const original = "Complete original audio"
			audioSaveControllerResult(t, m, original)
			folder := t.TempDir()
			path := filepath.Join(folder, "My exact filename.custom")
			require.NoError(t, os.WriteFile(path, []byte("previous contents"), 0600))
			m.openSave()
			m.path.SetValue(filepath.Base(path))
			m.saveFolder.SetValue(folder)
			if pastedPath {
				m.path.SetValue(path)
				m.saveFolder.SetValue("")
			}
			m.focusSave(2)
			message := audioSaveControllerRun(t, audioReviewKey(m, tea.KeyEnter))
			require.NoError(t, message.err)
			require.Equal(t, path, message.path)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, original, string(data))
		})
	}
}

func TestAudioSaveControllerValidationAndMissingFolder(t *testing.T) {
	audioSaveTestHome(t)
	m := audioReviewModel(t)
	audioSaveControllerResult(t, m, "Original audio")
	m.openSave()
	for _, name := range []string{"", "-", "CON.wav", "bad\n.wav"} {
		m.path.SetValue(name)
		require.Nil(t, audioReviewKey(m, tea.KeyEnter))
		require.Equal(t, "Enter a filename for saving.", m.note)
		require.False(t, m.busy)
	}
	m.path.SetValue("retained.wav")
	m.focusSave(1)
	m.saveFolder.SetValue("")
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.Equal(t, 1, m.saveFocus)
	require.Equal(t, "Enter a folder for saving.", m.note)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	m.saveFolder.SetValue(audioDefaultSaveFolder)
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.Contains(t, m.note, "Could not find your home folder")
	missing := filepath.Join(t.TempDir(), "missing")
	m.saveFolder.SetValue(missing)
	audioReviewKey(m, tea.KeyEnter)
	require.Equal(t, 2, m.saveFocus)
	message := audioSaveControllerRun(t, audioReviewKey(m, tea.KeyEnter))
	require.ErrorIs(t, message.err, os.ErrNotExist)
	_, err := os.Stat(missing)
	require.ErrorIs(t, err, os.ErrNotExist, "Custom folders must not be created implicitly")
}

func TestAudioSaveDestinationKeepsTildeAndExplicitParentSemantics(t *testing.T) {
	_, _, invalidErr := audioSaveDestination("bad\x00.wav", audioDefaultSaveFolder)
	require.EqualError(t, invalidErr, "Enter a filename for saving.")
	home := audioSaveTestHome(t)
	path, useDefault, err := audioSaveDestination("renamed.wav", audioDefaultSaveFolder+"/")
	require.NoError(t, err)
	require.True(t, useDefault)
	require.Equal(t, filepath.Join(home, "Downloads", "gpt-audio", "renamed.wav"), path)
	path, useDefault, err = audioSaveDestination("~/custom.wav", "")
	require.NoError(t, err)
	require.False(t, useDefault)
	require.Equal(t, filepath.Join(home, "custom.wav"), path)
	folder := "link" + string(filepath.Separator) + ".."
	path, useDefault, err = audioSaveDestination("exact", folder)
	require.NoError(t, err)
	require.False(t, useDefault)
	require.Equal(t, folder+string(filepath.Separator)+"exact", path)
}

func TestAudioSaveControllerLongNameCollisionHasUsefulError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("This case exercises the Unix 255-byte component boundary.")
	}
	home := audioSaveTestHome(t)
	m := audioReviewModel(t)
	const original = "Complete original audio"
	audioSaveControllerResult(t, m, original)
	name := strings.Repeat("a", 251) + ".wav"
	m.openSave()
	m.path.SetValue(name)
	first := audioSaveControllerRun(t, audioReviewKey(m, tea.KeyEnter))
	require.NoError(t, first.err)
	m.Update(first)
	m.openSave()
	m.path.SetValue(name)
	second := audioSaveControllerRun(t, audioReviewKey(m, tea.KeyEnter))
	require.ErrorIs(t, second.err, syscall.ENAMETOOLONG)
	m.Update(second)
	require.Equal(t, "Save path is too long.", m.failure.Summary)
	require.Contains(t, m.View().Content, "Use a shorter filename or folder.")
	data, err := os.ReadFile(filepath.Join(home, "Downloads", "gpt-audio", name))
	require.NoError(t, err)
	require.Equal(t, original, string(data))
}

func TestAudioSaveControllerConfirmsSuggestedDestination(t *testing.T) {
	home := audioSaveTestHome(t)
	m := audioReviewModel(t)
	const original = "Complete original audio, including the beginning and end."
	audioSaveControllerResult(t, m, original)
	m.openSave()
	require.Equal(t, 2, m.saveFocus)
	require.False(t, m.path.Focused())
	require.False(t, m.saveFolder.Focused())
	message := audioSaveControllerRun(t, audioReviewKey(m, tea.KeyEnter))
	require.NoError(t, message.err)
	path := filepath.Join(home, "Downloads", "gpt-audio", "original-retained-prompt.wav")
	require.Equal(t, path, message.path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, string(data))
}
