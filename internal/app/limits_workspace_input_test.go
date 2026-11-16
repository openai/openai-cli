package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestLimitsWorkspaceInputEscapesDirectionalControls(t *testing.T) {
	for _, width := range []int{40, 80} {
		for _, focused := range []bool{false, true} {
			for _, control := range []rune{'\u061c', '\u200e', '\u200f', '\u2028', '\u2029', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069'} {
				t.Run(fmt.Sprintf("width%d/focused%t/U%04X", width, focused, control), func(t *testing.T) {
					m, backend := newLimitsController(t, width, 24)
					m.begin("alert_update", gjson.Parse(limitsControllerAlert))
					value := "A" + string(control) + "雪B"
					m.draft.inputs[2].SetValue(value)
					m.setFocus(2)
					if !focused {
						m.setFocus(1)
					}
					m.draft.inputs[2].SetCursor(2)
					view := ansi.Strip(m.View().Content)
					require.NotContains(t, view, string(control))
					require.Contains(t, view, readable.Text(value))
					require.Equal(t, value, m.draft.inputs[2].Value())
					require.Equal(t, 2, m.draft.inputs[2].Position())
					require.Equal(t, focused, m.draft.inputs[2].Focused())
					change, err := m.draft.validate()
					require.NoError(t, err)
					require.Equal(t, value, *change.SubjectPrefix)
					require.Zero(t, backend.writes.Load())
				})
			}
		}
	}
}

func TestLimitsWorkspaceInputEscapeKeepsEditingAndOverflow(t *testing.T) {
	t.Run("expanded_controls_reset_raw_viewport", func(t *testing.T) {
		m, _ := newLimitsController(t, 40, 24)
		m.begin("alert_update", gjson.Parse(limitsControllerAlert))
		m.setFocus(2)
		value := strings.Repeat("\u202e", 20) + strings.Repeat("x", 24)
		m.draft.inputs[2].SetValue(value)
		m.draft.inputs[2].SetCursor(7)
		m.View()
		view := limitsInputView(m.draft.inputs[2])
		require.LessOrEqual(t, ansi.StringWidth(view), m.draft.inputs[2].Width()+1)
		require.NotContains(t, view, "\u202e")
		require.Contains(t, ansi.Strip(view), `\u202e`)
		require.Equal(t, value, m.draft.inputs[2].Value())
		require.Equal(t, 7, m.draft.inputs[2].Position())
	})
	for _, width := range []int{40, 80} {
		t.Run(fmt.Sprintf("width%d", width), func(t *testing.T) {
			m, backend := newLimitsController(t, width, 24)
			m.begin("alert_update", gjson.Parse(limitsControllerAlert))
			m.setFocus(2)
			value := strings.Repeat("long", 30) + "A\u202e雪B"
			m.draft.inputs[2].SetValue(value)
			m.draft.inputs[2].CursorEnd()
			view := ansi.Strip(m.View().Content)
			require.NotContains(t, view, "\u202e")
			require.Contains(t, view, `A\u202e雪B`)
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), width)
			}
			// Move across the raw rune, then delete it. Escaping must not enter the draft.
			m.draft.inputs[2].SetCursor(len([]rune(value)) - 2)
			m.View()
			limitsControllerKey(m, tea.KeyBackspace)
			require.Equal(t, strings.Replace(value, "\u202e", "", 1), m.draft.inputs[2].Value())
			m.View()
			require.Equal(t, m.draft.inputs[2].View(), limitsInputView(m.draft.inputs[2]))
			require.Zero(t, backend.writes.Load())
		})
	}
}
