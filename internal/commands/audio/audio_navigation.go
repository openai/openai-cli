package audio

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

// Keep keyboard traversal in the same order as the visible form.
func (m *audioWorkbench) focusSection(focus int) tea.Cmd {
	if focus == 4 && (m.focus != 4 || m.result == nil) {
		m.waveformFocused = false
	}
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
	if m.focus == audioFocusCommand {
		if backward {
			return m.focusSection(2)
		}
		return m.focusSection(4)
	}
	if m.inlineActions() {
		if m.focus == 4 && backward && m.setting == audioSettingMore {
			m.modal = "more"
			m.moreChoice = min(m.moreChoice, len(m.moreItems())-1)
			return m.focusSection(2)
		}
		if m.focus == 2 && !backward || m.focus == 4 && backward {
			return m.openActions()
		}
		if m.setting == audioSettingMore || m.setting == audioSettingHistory {
			m.setting = 1
		}
	}
	order := []int{0, 1, 2, 4}
	if !m.instructionsSupported() {
		order = []int{0, 2, 4}
	}
	step := 1
	if backward {
		step = -1
	}
	return m.focusSection(order[(slices.Index(order, m.focus)+step+len(order))%len(order)])
}

func (m *audioWorkbench) settingOrder() []int {
	order := []int{1, audioSettingDisclosure}
	if m.settingsExpanded {
		order = append(order, 0, 2, 3)
	}
	order = append(order, 4)
	if !m.inlineActions() {
		order = append(order, audioSettingHistory, audioSettingMore)
	}
	return order
}

// Down enters the first available part of Audio. Tab still lands on its controls.
func (m *audioWorkbench) enterAudioFromAbove() tea.Cmd {
	cmd := m.focusSection(4)
	m.waveformFocused = m.result.hasWaveform()
	if !m.waveformFocused {
		m.resultAction = 0
	}
	return cmd
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
				m.setting = 1
				return true, m.focusSection(2)
			}
			return true, m.focusSection(1)
		}
	case 2:
		order := m.settingOrder()
		onActionRow := m.setting >= 4 && m.setting <= audioSettingMore && (m.inlineActions() || len(m.formActionRows()) == 1)
		switch key {
		case "left", "right":
			if m.inlineActions() && (m.setting == 4 || m.setting == audioSettingDisclosure) {
				if key == "right" {
					return true, m.openActions()
				}
				return true, nil
			}
			if !onActionRow {
				return false, nil
			}
			step := 1
			if key == "left" {
				step = -1
			}
			m.setting = max(4, min(audioSettingMore, m.setting+step))
		case "up":
			index := slices.Index(order, m.setting)
			if onActionRow {
				index = slices.Index(order, 4)
			}
			if index <= 0 {
				if !m.instructionsSupported() {
					return true, m.focusSection(0)
				}
				return true, m.focusSection(1)
			}
			m.setting = order[index-1]
		case "down":
			index := slices.Index(order, m.setting)
			if index == len(order)-1 || onActionRow {
				return true, m.enterAudioFromAbove()
			}
			m.setting = order[index+1]
		case "home":
			m.setting = order[0]
		case "end":
			if m.inlineActions() {
				cmd := m.openActions()
				m.moreChoice = len(m.moreItems()) - 1
				return true, cmd
			}
			m.setting = audioSettingMore
		default:
			return false, nil
		}
		return true, nil
	case 4:
		switch key {
		case "left", "right":
			if m.waveformFocused {
				return false, nil
			}
			step := 1
			if key == "left" {
				step = -1
			}
			m.resultAction = max(0, min(1, m.resultAction+step))
		case "up":
			if !m.waveformFocused && m.result.hasWaveform() {
				m.waveformFocused = true
			} else {
				m.waveformFocused = false
				// Retrace a vertical move to its action. Other routes use the bottom form row.
				if !m.inlineActions() && len(m.formActionRows()) != 1 {
					m.setting = audioSettingMore
				} else if m.setting < 4 || m.setting > audioSettingMore {
					m.setting = 4
				}
				m.moreChoice = min(m.moreChoice, len(m.moreItems())-1)
				return true, m.focusSection(2)
			}
		case "down":
			if m.waveformFocused {
				m.waveformFocused = false
			} else {
				m.resultAction = min(1, m.resultAction+1)
			}
		case "home":
			m.waveformFocused, m.resultAction = false, 0
		case "end":
			m.waveformFocused, m.resultAction = false, 1
		default:
			return false, nil
		}
		return true, nil
	}
	return false, nil
}
