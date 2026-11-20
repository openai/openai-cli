package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func historyControl(m *audioWorkbench, key rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: key, Mod: tea.ModCtrl})
	return cmd
}

func TestCompactAudioHistoryKeepsRecordingAndControls(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 40, 12
	clip := audioReviewResult("retained", false)
	clip.entry.CreatedAt = time.Now()
	clip.savedPaths = []string{"/tmp/example.wav"}
	m.result = &clip
	m.entries = []audiohistory.Entry{clip.entry}
	m.modal = "history"
	m.historySearch.showFolder = true
	m.setHistoryQuery("Synthetic")
	view := m.View().Content
	for _, label := range []string{"Synthetic text", "Saved:", "History:", "Ctrl+P autoplay off", "Ctrl+D clear history", "Ctrl+L folder"} {
		require.Contains(t, view, label)
	}
}

func TestAudioHistoryAutoplayAndConfirmedClear(t *testing.T) {
	m := audioReviewModel(t)
	wav := audioSeekWave(make([]byte, 256), 8000, 1)
	settings := audioDefaultSettings()
	settings.Input = "Synthetic script"
	clip, err := m.store.Capture(t.Context(), io.NopCloser(bytes.NewReader(wav)), settings)
	require.NoError(t, err)
	settings.Preview = true
	preview, err := m.store.Capture(t.Context(), io.NopCloser(bytes.NewReader(wav)), settings)
	require.NoError(t, err)

	m.Update(audioSeekWorker(t, m.history()))
	require.Equal(t, []audiohistory.Entry{clip}, m.entries)
	require.Contains(t, m.View().Content, "Ctrl+P autoplay off")
	_, play := m.Update(audioSeekWorker(t, audioReviewKey(m, tea.KeyEnter)))
	require.Nil(t, play)
	require.Equal(t, clip.ID, m.result.entry.ID)
	require.False(t, m.playing)

	m.Update(audioSeekWorker(t, m.history()))
	m.Update(audioSeekWorker(t, historyControl(m, 'p')))
	require.Equal(t, "history", m.modal)
	require.Contains(t, m.View().Content, "Ctrl+P autoplay on")
	reopened := audioReviewModel(t)
	reopened.store = m.store
	reopened.Update(audioSeekWorker(t, reopened.history()))
	require.True(t, reopened.historyAutoplay)
	require.False(t, reopened.playing)
	_, play = m.Update(audioSeekWorker(t, audioReviewKey(m, tea.KeyEnter)))
	require.Equal(t, clip.ID, m.result.entry.ID)
	require.True(t, m.playing)
	m.stopPlayback()
	require.ErrorIs(t, audioSeekWorker(t, play).err, context.Canceled)

	m.input.SetValue("New unsaved draft")
	m.priorResult = &audioWorkResult{entry: preview}
	m.Update(audioSeekWorker(t, m.history()))
	for _, cancel := range []rune{tea.KeyEnter, tea.KeyEscape} {
		require.Nil(t, historyControl(m, 'd'))
		require.Contains(t, m.View().Content, "Clear all recording history?")
		require.Contains(t, m.View().Content, "Y clear all")
		require.Nil(t, audioReviewKey(m, cancel))
		require.Equal(t, "history", m.modal)
		records, err := m.store.List(t.Context())
		require.NoError(t, err)
		require.Len(t, records, 2)
	}
	external := filepath.Join(t.TempDir(), "export.wav")
	require.NoError(t, os.WriteFile(external, wav, 0600))
	require.Nil(t, historyControl(m, 'd'))
	clear := audioReviewKey(m, 'y')
	require.Contains(t, m.View().Content, "Clearing History")
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Equal(t, "clear-history", m.modal)
	m.Update(audioSeekWorker(t, clear))
	require.Equal(t, "history", m.modal)
	require.Nil(t, m.result)
	require.Nil(t, m.priorResult)
	require.Empty(t, m.entries)
	require.Contains(t, m.View().Content, "History cleared.")
	require.Contains(t, m.View().Content, "Ctrl+P autoplay on")
	require.Equal(t, "New unsaved draft", m.input.Value())
	records, err := m.store.List(t.Context())
	require.NoError(t, err)
	require.Empty(t, records)
	export, err := os.ReadFile(external)
	require.NoError(t, err)
	require.Equal(t, wav, export)
}

func TestAudioHistoryPartialClearPreservesRemainingRecording(t *testing.T) {
	m := audioReviewModel(t)
	capture := func(preview bool) audiohistory.Entry {
		t.Helper()
		settings := audioDefaultSettings()
		settings.Input, settings.Preview = "Synthetic clip", preview
		clip, err := m.store.Capture(t.Context(), io.NopCloser(bytes.NewReader([]byte("synthetic audio"))), settings)
		require.NoError(t, err)
		return clip
	}
	keep, clear := capture(false), capture(true)
	foreign := filepath.Join(m.store.Directory, keep.ID, "unrelated")
	require.NoError(t, os.WriteFile(foreign, []byte("keep"), 0600))
	m.result = &audioWorkResult{entry: clear}
	m.priorResult = &audioWorkResult{entry: keep}
	m.Update(audioSeekWorker(t, m.history()))
	m.setHistoryQuery("synthetic")
	require.Nil(t, historyControl(m, 'd'))
	msg := audioSeekWorker(t, audioReviewKey(m, 'y'))
	require.Error(t, msg.err)
	m.Update(msg)
	require.Equal(t, "history", m.modal)
	require.ErrorIs(t, m.actionError(), msg.err)
	require.Equal(t, keep.ID, m.result.entry.ID)
	require.Nil(t, m.priorResult)
	require.Equal(t, "synthetic", m.historySearch.query)
	require.Contains(t, m.View().Content, audioHistoryClearFailureNote)
	require.Contains(t, m.View().Content, "Synthetic clip")
	require.Contains(t, m.View().Content, "F1 details")
	audioReviewKey(m, tea.KeyF1)
	require.Equal(t, "recovery", m.modal)
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, "history", m.modal)
	require.Equal(t, []audiohistory.Entry{keep}, m.entries)
	require.FileExists(t, foreign)

	// A successful clear resolves the clear error and leaves other failures visible at exit.
	exportFailure := errors.New("synthetic export failure")
	m.Update(audioWorkMessage{kind: "export", err: exportFailure})
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, "history", m.modal)
	require.NoError(t, os.Remove(foreign))
	require.Nil(t, historyControl(m, 'd'))
	retry := audioSeekWorker(t, audioReviewKey(m, 'y'))
	require.NoError(t, retry.err)
	m.Update(retry)
	require.Empty(t, m.entries)
	require.ErrorIs(t, m.actionError(), exportFailure)
	require.NotErrorIs(t, m.actionError(), msg.err)
}

func TestAudioHistoryAutoplayPreferenceFailureKeepsListUsable(t *testing.T) {
	m := audioReviewModel(t)
	m.width, m.height = 40, 12
	settings := audioDefaultSettings()
	settings.Input = "Synthetic clip"
	entry, err := m.store.Capture(t.Context(), io.NopCloser(bytes.NewReader([]byte("synthetic audio"))), settings)
	require.NoError(t, err)
	m.Update(audioSeekWorker(t, m.history()))
	marker := filepath.Join(m.store.Directory, ".autoplay")
	require.NoError(t, os.WriteFile(marker, []byte("unexpected"), 0600))
	var failures []error
	for _, operation := range []struct {
		run       func() tea.Cmd
		note      string
		readError bool
	}{
		{func() tea.Cmd { return historyControl(m, 'p') }, audioHistoryAutoplayFailureNote, false},
		{m.history, audioHistoryAutoplayReadFailureNote, true},
	} {
		msg := audioSeekWorker(t, operation.run())
		failure := msg.err
		if operation.readError {
			failure = msg.autoplayErr
		}
		require.ErrorIs(t, failure, audiohistory.ErrInvalid)
		m.Update(msg)
		require.ErrorIs(t, m.actionError(), failure)
		failures = append(failures, failure)
		require.Equal(t, "history", m.modal)
		require.False(t, m.historyAutoplay)
		require.Equal(t, []audiohistory.Entry{entry}, m.entries)
		view := m.View().Content
		require.Contains(t, view, "Synthetic clip")
		require.Contains(t, view, operation.note)
		require.Contains(t, view, "F1 details")
	}
	require.NoError(t, os.Remove(marker))
	m.Update(audioSeekWorker(t, m.history()))
	require.ErrorIs(t, m.actionError(), failures[0])
	require.NotErrorIs(t, m.actionError(), failures[1])
	m.Update(audioSeekWorker(t, historyControl(m, 'p')))
	require.NoError(t, m.actionError())
	require.True(t, m.historyAutoplay)
}
