package audio

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioStudioComposerKeepsScriptSpaceAndDeliveryColumns(t *testing.T) {
	for _, size := range [][2]int{{110, 28}, {133, 39}} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		require.True(t, m.studioLayout())
		rows, _, active := m.studioComposerRows()
		require.Equal(t, -1, active)
		require.Equal(t, "› Script", ansi.Strip(rows[0]))
		deliveryRow := -1
		for i, row := range rows {
			if strings.TrimSpace(ansi.Strip(row)) == "Delivery" {
				deliveryRow = i
			}
		}
		require.Positive(t, deliveryRow)

		m.input.SetValue(strings.Repeat("界文 ", 30))
		m.instructions.SetValue("Speak softly.")
		m.focusSection(2)
		m.settingsExpanded, m.setting = true, 1
		m.resizeEditors()
		rows, _, active = m.studioComposerRows()
		require.Equal(t, "  Delivery", ansi.Strip(rows[deliveryRow]))
		require.Contains(t, ansi.Strip(rows[active]), "› Voice")
		require.Contains(t, strings.Join(rows, "\n"), "Speak softly.")
		require.Contains(t, strings.Join(rows, "\n"), "Coral")
		for _, row := range rows {
			require.LessOrEqual(t, ansi.StringWidth(row), m.studioScriptWidth())
			for _, value := range []string{"Speak softly.", "Coral"} {
				plain := ansi.Strip(row)
				if index := strings.Index(plain, value); index != -1 {
					require.Equal(t, m.formLabelWidth()+3, ansi.StringWidth(plain[:index]))
				}
			}
		}
	}
}

func TestAudioStudioComposerPlacesAllEditorCarets(t *testing.T) {
	m := audioReviewModel(t)
	for _, focus := range []int{0, 1, 2} {
		m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
		m.focusSection(focus)
		editor := &m.input
		if focus == 1 {
			editor = &m.instructions
		} else if focus == 2 {
			m.setting, m.settingsExpanded = 3, true
			m.beginInlineSpeed()
			editor = &m.speedInput
		}
		editor.SetValue("界🙂e\u0301Z")
		editor.MoveToEnd()
		editor.SetCursorColumn(editor.Column() - 1)
		m.resizeEditors()
		rows, cursor, _ := m.studioComposerRows()
		require.NotNil(t, cursor)
		line := ansi.Strip(rows[cursor.Y])
		index := strings.LastIndex(line, "Z")
		require.NotEqual(t, -1, index)
		require.Equal(t, ansi.StringWidth(line[:index]), cursor.X)
	}
}
