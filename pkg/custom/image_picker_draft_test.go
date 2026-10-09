package custom

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestImagePickerDraftRestoresEditablePromptAndSettings(t *testing.T) {
	for name, prompt := range map[string]string{
		"multiline Unicode": "  Synthetic moon 日本語 🌕\nsecond line\t'quoted'  ",
		"cleared prompt":    "",
	} {
		t.Run(name, func(t *testing.T) {
			path := pickerStatePath(t)
			want := pickerStateSettings(t)
			want.prompt, want.quality, want.count = prompt, "high", "3"
			require.NoError(t, saveImagePickerState(t.Context(), path, want))
			stored, found, err := loadImagePickerState(t.Context(), path)
			require.NoError(t, err)
			require.True(t, found)
			options := restoreImagePickerDraft(imagePickerOptions{Shell: "bash"}, stored)
			m, err := newImagePicker(options)
			require.NoError(t, err)
			require.Equal(t, want, m.settings)
			require.Equal(t, prompt, string(m.draft))
			require.Equal(t, len([]rune(prompt)), m.cursor)
			require.Equal(t, prompt != "", options.resuming)
			require.Equal(t, "prompt", m.focus)
			require.Empty(t, m.result.Args, "restoring a draft must not submit it")
			m.insertPrompt(" next edit")
			require.Equal(t, prompt+" next edit", m.settings.prompt)
			require.Equal(t, want, stored, "editing must not mutate the loaded snapshot")
		})
	}
}

func TestImagePickerDraftPreservesExplicitAndSessionInputs(t *testing.T) {
	stored := pickerStateSettings(t)
	stored.prompt, stored.count = "older saved prompt", "2"
	session := stored
	session.prompt, session.count = "session prompt", "4"
	explicit := stored
	explicit.prompt = "explicit prompt"
	cleared := session
	cleared.prompt = ""
	for _, test := range []struct {
		name    string
		options imagePickerOptions
		want    imagePickerSettings
	}{
		{"explicit prompt", imagePickerOptions{Prompt: "explicit prompt", Shell: "bash"}, explicit},
		{"session prompt", imagePickerOptions{Prompt: session.prompt, initial: &session, Shell: "zsh", resuming: true, initialNote: "Session note"}, session},
		{"cleared session prompt", imagePickerOptions{initial: &session, Shell: "fish", resuming: true}, cleared},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := restoreImagePickerDraft(test.options, stored)
			require.Equal(t, test.options.Prompt, options.Prompt)
			require.Equal(t, test.options.Shell, options.Shell)
			require.Equal(t, test.options.initialNote, options.initialNote)
			require.Equal(t, test.options.resuming, options.resuming)
			if test.options.initial != nil {
				require.Same(t, test.options.initial, options.initial)
			}
			m, err := newImagePicker(options)
			require.NoError(t, err)
			require.Equal(t, test.want, m.settings)
			require.Empty(t, m.result.Args)
		})
	}
	require.Equal(t, "session prompt", session.prompt)
	require.Equal(t, "older saved prompt", stored.prompt)
}

func TestImagePickerRestoredDraftKeepsQuietInputGuard(t *testing.T) {
	stored := pickerStateSettings(t)
	stored.prompt = "Restored synthetic draft"
	options := restoreImagePickerDraft(imagePickerOptions{Shell: "bash"}, stored)
	require.True(t, options.resuming)
	m, err := newImagePicker(options)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	file, err := os.CreateTemp(t.TempDir(), "picker-output")
	require.NoError(t, err)
	defer file.Close()
	p := &imagePickerInline{model: m, output: &imagePickerOutput{File: file, cancel: cancel}, resuming: options.resuming}
	for _, message := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl},
		tea.PasteMsg{Content: "queued paste"},
	} {
		_, command := p.Update(message)
		require.Nil(t, command)
		require.Equal(t, stored, m.settings)
		require.Empty(t, m.result.Args)
	}
	// A displayed restored draft still rejects queued submit keys during quiet time.
	p.started, p.submitAfter = true, time.Now().Add(time.Minute)
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter}, {Code: 'g', Mod: tea.ModCtrl}, {Code: 'p', Mod: tea.ModCtrl},
	} {
		_, command := p.Update(key)
		require.Nil(t, command, "blocked input must not schedule a later request")
		require.Empty(t, m.result.Args)
	}
	require.NoError(t, ctx.Err())
}

func TestImagePickerCanceledSnapshotTracksLiveDraftChanges(t *testing.T) {
	for _, changed := range []bool{false, true} {
		for _, signal := range []bool{false, true} {
			name := "untouched"
			if changed {
				name = "edited"
			}
			if signal {
				name += " SIGTERM"
			} else {
				name += " Ctrl+C"
			}
			t.Run(name, func(t *testing.T) {
				m := pickerForTest(t)
				initial := m.settings
				if changed {
					m.Update(tea.PasteMsg{Content: "\nupdated 雪 🌕"})
				}
				want := m.settings
				pickerKey(m, tea.KeyTab)
				pickerKey(m, tea.KeyDown)
				if signal {
					m.Update(imagePickerStopMsg{code: 143})
				} else {
					m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				}
				result := m.snapshotResult(initial)
				require.True(t, result.Canceled)
				require.Equal(t, changed, result.changed)
				require.Equal(t, want, result.settings)
				require.Empty(t, result.Args)
				require.False(t, result.PrintOnly)
				if signal {
					require.Equal(t, 143, result.ExitCode)
				} else {
					require.Zero(t, result.ExitCode, "the workflow owns Ctrl+C status conversion")
				}
			})
		}
	}
}

func TestImagePickerExitDraftSavesAfterParentCancellation(t *testing.T) {
	path := pickerStatePath(t)
	want := pickerStateSettings(t)
	want.prompt, want.count = "Canceled synthetic draft\n日本語", "3"
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	var diagnostics bytes.Buffer
	require.True(t, saveImagePickerExitDraft(parent, &diagnostics, path, want))
	require.ErrorIs(t, parent.Err(), context.Canceled, "saving must not change the parent cancellation")
	require.Empty(t, diagnostics.String())
	got, found, err := loadImagePickerState(t.Context(), path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
}

func TestImagePickerCanceledRevertedDraftDoesNotNeedSaving(t *testing.T) {
	m := pickerForTest(t)
	initial := m.settings
	m.Update(tea.PasteMsg{Content: "x"})
	pickerKey(m, tea.KeyBackspace)
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	result := m.snapshotResult(initial)
	require.True(t, result.Canceled)
	require.Equal(t, initial, result.settings)
	require.False(t, result.changed, "reverted edits must not overwrite a newer session's draft")
	require.Empty(t, result.Args)
}

func TestImagePickerExitDraftPreservesCorruptRecordAndPrivateDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-private-directory", "image-picker.json")
	require.NoError(t, os.Mkdir(filepath.Dir(path), 0700))
	corrupt := []byte(`{"version":99,"prompt":"synthetic-private-old-prompt"}`)
	require.NoError(t, os.WriteFile(path, corrupt, 0600))
	want := pickerStateSettings(t)
	want.prompt = "synthetic-private-new-prompt"
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	var diagnostics bytes.Buffer
	require.False(t, saveImagePickerExitDraft(parent, &diagnostics, path, want))
	require.Equal(t, "Could not save the draft.\n", diagnostics.String())
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, corrupt, after)
	// Even a failed warning cannot replace the original cancellation status.
	require.False(t, saveImagePickerExitDraft(parent, failedImagePickerWriter{}, path, want))
	require.ErrorIs(t, parent.Err(), context.Canceled)
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, corrupt, after)
}

func TestImagePickerExitDraftDoesNotCommitUnconfirmedChoices(t *testing.T) {
	for _, pending := range []string{"highlighted count", "folder path"} {
		t.Run(pending, func(t *testing.T) {
			m := pickerForTest(t)
			m.settings.outputDir = t.TempDir()
			initial := m.settings
			m.Update(tea.PasteMsg{Content: " changed draft"})
			want := m.settings
			switch pending {
			case "highlighted count":
				m.focus = "options"
				m.choose("count")
				pickerKey(m, tea.KeyEnd)
				require.Equal(t, "10", m.rows()[m.selected].value)
				require.Equal(t, "1", m.settings.count)
			case "folder path":
				m.openFolderEditor(m.settings.outputDir)
				m.insertFolderPath("/unconfirmed-synthetic-path")
				require.NotEqual(t, m.settings.outputDir, string(m.folder.draft))
			}
			m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			result := m.snapshotResult(initial)
			require.True(t, result.Canceled)
			require.True(t, result.changed)
			require.Empty(t, result.Args)
			path := filepath.Join(t.TempDir(), "image-picker.json")
			var diagnostics bytes.Buffer
			require.True(t, saveImagePickerExitDraft(t.Context(), &diagnostics, path, result.settings))
			got, found, err := loadImagePickerState(t.Context(), path)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, want, got, "only confirmed settings belong to the saved draft")
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestImagePickerDraftEditClearsSavedStatus(t *testing.T) {
	for _, key := range []rune{tea.KeyBackspace, tea.KeyDelete, 'u'} {
		m := pickerForTest(t)
		m.note = "Draft saved."
		before := m.settings.prompt
		message := tea.KeyPressMsg{Code: key}
		if key == tea.KeyDelete {
			m.cursor = 0
		} else if key == 'u' {
			message.Mod = tea.ModCtrl
		}
		m.Update(message)
		require.NotEqual(t, before, m.settings.prompt)
		require.Empty(t, m.note, "changed text must not claim its earlier snapshot is saved")
	}
}
