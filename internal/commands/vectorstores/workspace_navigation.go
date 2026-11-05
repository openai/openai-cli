package vectorstores

import tea "charm.land/bubbletea/v2"

func (m *workspace) enterWithNote(page, note string) tea.Cmd {
	cmd := m.enter(page)
	m.note = note
	return cmd
}

// Store rows and store actions form one vertical keyboard path.
func (m *workspace) storeChoices() []int {
	choices := []int{1, 2}
	if m.storeNext != "" {
		choices = append(choices, 3)
	}
	return append(choices, 4)
}

func (m *workspace) navigate(key string) (tea.Cmd, bool) {
	delta := 1
	if key == "up" || key == "left" {
		delta = -1
	}
	vertical := key == "up" || key == "down"
	if m.page == "stores" && vertical {
		count, choices := m.listLength(), m.storeChoices()
		index := m.selected
		if m.focus != 0 {
			for i, focus := range choices {
				if focus == m.focus {
					index = count + i
				}
			}
		}
		index = max(0, min(count+len(choices)-1, index+delta))
		if index < count {
			m.focus, m.selected = 0, index
		} else {
			m.focus = choices[index-count]
		}
		return m.syncFocus(), true
	}
	if m.page == "stores" {
		return nil, false
	}
	if m.page == "passage" || m.page == "document" || m.focusCount() == 1 {
		return nil, false
	}
	lastAction := m.focusCount() - 1
	if m.page == "home" {
		lastAction = 5
	}
	if !vertical {
		if m.editing() || m.focus == 0 || m.focus > lastAction {
			return nil, false
		}
		m.focus = 1 + (m.focus-1+delta+lastAction)%lastAction
		return m.syncFocus(), true
	}
	if m.page == "home" {
		switch {
		case m.focus == 0:
			m.focus = 1
		case m.focus <= 5:
			m.focus = 0
			if delta > 0 && len(m.results) > 0 {
				m.focus = matchListFocus
			}
		case delta < 0 && m.resultSelected == 0:
			m.focus = 0
		default:
			return nil, false
		}
		return m.syncFocus(), true
	}
	_, form := managementForms[m.page]
	if m.page == "create" || m.page == "upload" || form {
		if m.focus == 0 {
			m.focus = 1
		} else {
			m.focus = 0
		}
		return m.syncFocus(), true
	}
	// File lists place their action row above the selectable items.
	if m.focus > 0 {
		m.focus = 0
		return m.syncFocus(), true
	}
	if delta < 0 && m.selected == 0 || m.listLength() == 0 {
		m.focus = 1
		return m.syncFocus(), true
	}
	return nil, false
}
