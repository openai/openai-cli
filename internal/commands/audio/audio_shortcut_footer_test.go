package audio

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const audioEditorFooterText = "Enter Generate  Tab Next  Esc Exit"

func TestAudioKeyboardShortcutsBelowFrame(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		setup         func(*audioWorkbench)
		body, hints   []string
		cursorRow     string
	}{
		{
			name: "studio", width: 137, height: 41,
			body: []string{"Script", "Voice studio", "Generate speech"}, hints: []string{audioEditorFooterText},
			cursorRow: "Synthetic draft",
		},
		{
			name: "stacked", width: 60, height: 24,
			body: []string{"Text to speech", "Generate speech"}, hints: []string{audioEditorFooterText},
			cursorRow: "Synthetic draft",
		},
		{
			name: "history", width: 60, height: 24,
			setup: func(m *audioWorkbench) {
				m.modal = "history"
				m.entries = append(m.entries, m.result.entry)
				m.result.savedPaths = []string{"/exports/speech.wav"}
				m.store.Directory = "/cache/recordings"
				m.historySearch.showFolder, m.historySearch.focused = true, true
			},
			body:  []string{"Coral  WAV", "Saved: /exports/speech.wav", "History: /cache/recordings"},
			hints: []string{"↓ recordings", "Enter open", "Esc back", "Ctrl+L folder"}, cursorRow: "Search",
		},
		{
			name: "save", width: 80, height: 24, setup: func(m *audioWorkbench) { m.openSave() },
			body: []string{"Filename", "Full recording"}, hints: []string{"Tab next", "Enter done", "Esc back"},
			cursorRow: "Save to",
		},
		{
			name: "command", width: 60, height: 24, setup: func(m *audioWorkbench) { m.focusCommand() },
			body: []string{"Command", "speech.wav"}, hints: []string{"Ctrl+P print/exit", "Esc back"},
		},
		{
			name: "retained-generation-error", width: 110, height: 28,
			setup: func(m *audioWorkbench) {
				m.retainedGenerationFailure = true
				m.failure.Summary = "Could not generate new audio."
				m.focusSection(4)
			},
			body: []string{"Could not generate new audio."}, hints: []string{"F1 details", "Esc back"},
		},
		{
			name: "seek-validation", width: 110, height: 28,
			setup: func(m *audioWorkbench) {
				m.focusSection(4)
				audioReviewKey(m, '9')
				audioReviewKey(m, tea.KeyEnter)
			},
			body: []string{"Go to time", "Time exceeds the recording."}, hints: []string{"Enter apply", "Esc cancel"},
			cursorRow: "Go to time",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := audioReviewModel(t)
			result := audioReviewResult("footer", false)
			m.result = &result
			m.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			m.input.SetValue("Synthetic draft")
			if tc.setup != nil {
				tc.setup(m)
			}
			m.resizeEditors()
			plain := m.View()
			require.NotContains(t, plain.Content, "38;2;")
			require.NotContains(t, plain.Content, "48;2;")
			m.color, m.dark = true, true
			view := m.View()
			require.Equal(t, ansi.Strip(plain.Content), ansi.Strip(view.Content))
			require.Equal(t, plain.Cursor, view.Cursor)
			rows := strings.Split(ansi.Strip(view.Content), "\n")
			require.LessOrEqual(t, len(rows), tc.height)
			var footerStart int
			for i, row := range rows {
				require.Less(t, ansi.StringWidth(row), tc.width)
				if strings.HasPrefix(row, "╰") && strings.HasSuffix(row, "╯") {
					footerStart = i + 1
				}
			}
			require.Positive(t, footerStart, "missing bottom border")
			require.Less(t, footerStart, len(rows), "no shortcut rows below the bottom border")
			body := strings.Join(rows[:footerStart-1], "\n")
			footer := strings.Join(rows[footerStart:], "\n")
			for _, label := range tc.body {
				require.Contains(t, body, label)
				require.NotContains(t, footer, label)
			}
			for _, hint := range tc.hints {
				require.Contains(t, footer, hint)
				require.NotContains(t, body, hint)
			}
			if tc.cursorRow != "" {
				require.NotNil(t, view.Cursor)
				require.Greater(t, view.Cursor.Y, 0)
				require.Less(t, view.Cursor.Y, footerStart-1)
				require.Contains(t, rows[view.Cursor.Y], tc.cursorRow)
			}
		})
	}
}

func TestAudioEditorFooterShowsOnlyPrimaryActions(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 60, Height: 24}, {Width: 137, Height: 41}} {
		m := audioReviewModel(t)
		m.Update(size)
		for _, focus := range []int{0, 1} {
			m.focusSection(focus)
			rows := strings.Split(ansi.Strip(m.View().Content), "\n")
			require.Equal(t, audioEditorFooterText, strings.TrimSpace(rows[len(rows)-1]), "focus %d, size %v", focus, size)
			if m.framed() {
				require.True(t, strings.HasPrefix(rows[len(rows)-2], "╰"), "only one row belongs below the frame")
			}
		}
	}

	m := audioReviewModel(t)
	result := audioReviewResult("previous-recording", false)
	m.result, m.retainedGenerationFailure = &result, true
	m.failure.Summary = "Could not generate new audio."
	for _, focus := range []int{0, 1} {
		m.focusSection(focus)
		rows := strings.Split(ansi.Strip(m.View().Content), "\n")
		require.Equal(t, audioEditorFooterText+"  F1 details", strings.TrimSpace(rows[len(rows)-1]))
	}
}

func TestAudioCompactShortcutsKeepDraftCaretVisible(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(tea.PasteMsg{Content: strings.Repeat("wrapped input ", 15) + "tailZ"})
	audioReviewKey(m, tea.KeyLeft)
	view := m.View()
	assertAudioNativeCursorBefore(t, view, "Z")
	rows := strings.Split(ansi.Strip(view.Content), "\n")
	require.LessOrEqual(t, len(rows), m.height-1)
	for _, row := range rows {
		require.Less(t, ansi.StringWidth(row), m.width)
	}
	require.NotContains(t, view.Content, "╰")
	require.Less(t, view.Cursor.Y, len(rows)-1)
	require.Equal(t, audioEditorFooterText, strings.TrimSpace(rows[len(rows)-1]))
}
