package custom

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

// Keep keyboard traversal in the same order as the visible form.
func (m *audioWorkbench) focusSection(focus int) tea.Cmd {
	m.input.Blur()
	m.instructions.Blur()
	m.focus = focus
	switch focus {
	case 0:
		return m.input.Focus()
	case 1:
		return m.instructions.Focus()
	case 3:
		m.previewOffset = min(m.previewOffset, m.previewLimit())
	}
	return nil
}

func (m *audioWorkbench) cycleSection(backward bool) tea.Cmd {
	order := []int{0, 1, 2, 3}
	if m.result != nil {
		order = []int{0, 1, 2, 4, 3}
	}
	step := 1
	if backward {
		step = -1
	}
	return m.focusSection(order[(slices.Index(order, m.focus)+step+len(order))%len(order)])
}

func (m *audioWorkbench) previewLimit() int {
	return max(0, ansi.StringWidth(readable.Text(m.commandPreview()))-m.audioWidth()+2)
}

// Call after busy, minimum-size, and modal guards. Editing keeps modified arrows
// and active selections; plain arrows leave only the first or last visual row.
func (m *audioWorkbench) navigateSection(key string) (bool, tea.Cmd) {
	if key == "tab" || key == "shift+tab" {
		return true, m.cycleSection(key == "shift+tab")
	}
	switch m.focus {
	case 0, 1:
		if key != "up" && key != "down" {
			return false, nil
		}
		editor := &m.input
		if m.focus == 1 {
			editor = &m.instructions
		}
		if editor.HasSelection() {
			return false, nil
		}
		line := editor.LineInfo()
		if key == "up" && editor.Line() == 0 && line.RowOffset == 0 && m.focus == 1 {
			return true, m.focusSection(0)
		}
		if key == "down" && editor.Line() == editor.LineCount()-1 && line.RowOffset == line.Height-1 {
			if m.focus == 1 {
				m.setting = 0
			}
			return true, m.focusSection(m.focus + 1)
		}
	case 2:
		switch key {
		case "up":
			if m.setting == 0 {
				return true, m.focusSection(1)
			}
			m.setting--
		case "down":
			if m.setting == 7 {
				if m.result != nil {
					return true, m.focusSection(4)
				}
				m.previewOffset = 0
				return true, m.focusSection(3)
			}
			m.setting++
		case "home":
			m.setting = 0
		case "end":
			m.setting = 7
		default:
			return false, nil
		}
		return true, nil
	case 4:
		switch key {
		case "up":
			m.setting = 7
			return true, m.focusSection(2)
		case "down":
			m.previewOffset = 0
			return true, m.focusSection(3)
		}
	case 3:
		limit := m.previewLimit()
		m.previewOffset = min(m.previewOffset, limit)
		width := m.audioWidth() - 2
		switch key {
		case "up":
			if m.previewOffset == 0 {
				if m.result != nil {
					return true, m.focusSection(4)
				}
				m.setting = 7
				return true, m.focusSection(2)
			}
			m.previewOffset = max(0, m.previewOffset-width)
		case "down", "pgdown":
			m.previewOffset = min(limit, m.previewOffset+width)
		case "pgup":
			m.previewOffset = max(0, m.previewOffset-width)
		case "left":
			m.previewOffset = max(0, m.previewOffset-10)
		case "right":
			m.previewOffset = min(limit, m.previewOffset+10)
		case "home":
			m.previewOffset = 0
		case "end":
			m.previewOffset = limit
		default:
			return false, nil
		}
		return true, nil
	}
	return false, nil
}
