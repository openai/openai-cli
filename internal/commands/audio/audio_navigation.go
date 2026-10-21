package audio

import (
	"slices"

	tea "charm.land/bubbletea/v2"
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
	}
	return nil
}

func (m *audioWorkbench) cycleSection(backward bool) tea.Cmd {
	order := []int{0, 1, 2}
	if !m.instructionsSupported() {
		order = []int{0, 2}
	}
	if m.result != nil {
		order = append(order, 4)
	}
	step := 1
	if backward {
		step = -1
	}
	return m.focusSection(order[(slices.Index(order, m.focus)+step+len(order))%len(order)])
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
		// Bubbles cannot move Down into a cursor-only wrap row. Reach it
		// through the editor so its viewport and cursor refresh together.
		if key == "down" {
			end := *editor
			end.CursorEnd()
			last := end.LineInfo()
			if line.RowOffset+1 == last.RowOffset && last.StartColumn == end.Column() {
				var command tea.Cmd
				*editor, command = editor.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
				return true, command
			}
		}
		if key == "up" && editor.Line() == 0 && line.RowOffset == 0 && m.focus == 1 {
			return true, m.focusSection(0)
		}
		if key == "down" && editor.Line() == editor.LineCount()-1 && line.RowOffset == line.Height-1 {
			if m.focus == 1 || !m.instructionsSupported() {
				m.setting = 0
				return true, m.focusSection(2)
			}
			return true, m.focusSection(1)
		}
	case 2:
		switch key {
		case "up":
			if m.setting == 0 {
				if !m.instructionsSupported() {
					return true, m.focusSection(0)
				}
				return true, m.focusSection(1)
			}
			m.setting--
		case "down":
			if m.setting == 5 {
				if m.result != nil {
					m.resultAction = 0
					return true, m.focusSection(4)
				}
				return true, nil
			}
			m.setting++
		case "home":
			m.setting = 0
		case "end":
			m.setting = 5
		default:
			return false, nil
		}
		return true, nil
	case 4:
		switch key {
		case "up":
			if m.resultAction > 0 {
				m.resultAction--
			} else {
				m.setting = 5
				return true, m.focusSection(2)
			}
		case "down":
			m.resultAction = min(1, m.resultAction+1)
		case "home":
			m.resultAction = 0
		case "end":
			m.resultAction = 1
		default:
			return false, nil
		}
		return true, nil
	}
	return false, nil
}
