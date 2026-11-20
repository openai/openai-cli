package vectorstores

import tea "charm.land/bubbletea/v2"

func (m *workspace) enterWithNote(page, note string) tea.Cmd {
	cmd := m.enter(page)
	m.note = note
	return cmd
}

const storeFinderFocus = 4

func (m *workspace) nextStoreFocus(delta int) int {
	count := m.focusCount()
	next := (m.focus + delta + count) % count
	if next == 3 && m.storeNext == "" {
		next += delta
	}
	if next == 0 && m.listLength() == 0 {
		next = (count + delta) % count
	}
	return next
}

// Keep the picker navigable until the user types into the highlighted finder.
func (m *workspace) focusFinderForInput(message tea.Msg) tea.Cmd {
	if m.page != "stores" || m.focus != storeFinderFocus || m.busy || m.width < 40 || m.height < 12 {
		return nil
	}
	switch input := message.(type) {
	case tea.KeyPressMsg:
		if input.Text != "" && input.Text != "/" || input.Code == tea.KeyBackspace || input.Code == tea.KeyDelete {
			return m.enter("filter")
		}
	case tea.PasteMsg:
		if input.Content != "" {
			return m.enter("filter")
		}
	}
	return nil
}

func (m *workspace) applyStoreFilter() tea.Cmd {
	m.storeFilter = m.editor.Value()
	cmd := m.enter("stores")
	if len(m.stores) == 0 {
		m.focus = storeFinderFocus
	}
	return cmd
}

func (m *workspace) firstStoreFocus() int {
	if len(m.filteredStores()) > 0 {
		return 0
	}
	if m.storeFilter != "" {
		return storeFinderFocus
	}
	return 1 // Create store.
}

// The available actions follow the finder and store rows.
func (m *workspace) storeChoices() []int {
	choices := []int{1, 2}
	if m.storeNext != "" {
		choices = append(choices, 3)
	}
	return choices
}

func (m *workspace) browserFilterKey(key string) (tea.Cmd, bool) {
	switch key {
	case "/":
		if !m.editing() {
			m.focus, m.findingFile = 0, true
			m.editor.SetValue(m.browserFilter)
			m.editor.Placeholder = "Name (. includes hidden)"
			return m.syncFocus(), true
		}
	case "esc":
		if m.findingFile || m.browserFilter != "" {
			m.findingFile, m.focus = false, 0
			m.filterFolder("")
			if len(m.entries) == 0 {
				m.focus = 1
			}
			return m.syncFocus(), true
		}
	case "tab", "shift+tab", "up", "down", "pgup", "pgdown":
		if m.findingFile {
			m.findingFile = false
			m.syncFocus()
		}
	}
	return nil, false
}

func (m *workspace) moveListFocus(key string) tea.Cmd {
	items, actions := m.listLength(), m.focusCount()-1
	delta := 1
	if key == "up" || key == "shift+tab" || key == "left" {
		delta = -1
	}
	if key == "up" || key == "down" {
		index := m.selected
		if m.focus > 0 {
			index = items + m.focus - 1
		}
		index = max(0, min(index+delta, items+actions-1))
		if index < items {
			m.selected, m.focus = index, 0
		} else {
			m.focus = index - items + 1
		}
	} else {
		first := 0 // Tab skips the rest of the files to reach Actions.
		if items == 0 || key == "left" || key == "right" {
			first = 1
		}
		count := actions + 1 - first
		m.focus = first + (m.focus-first+delta+count)%count
	}
	return m.syncFocus()
}

func (m *workspace) navigate(key string) (tea.Cmd, bool) {
	delta := 1
	if key == "up" || key == "left" {
		delta = -1
	}
	vertical := key == "up" || key == "down"
	if m.page == "files" || m.page == "batchfiles" || m.page == "browse" {
		if vertical || m.focus != 0 {
			return m.moveListFocus(key), true
		}
		return nil, false
	}
	if m.page == "stores" && vertical {
		count, choices := m.listLength(), m.storeChoices()
		index := m.selected
		if m.focus == storeFinderFocus {
			index = -1
		} else if m.focus != 0 {
			for i, focus := range choices {
				if focus == m.focus {
					index = count + i
				}
			}
		}
		index = max(-1, min(count+len(choices)-1, index+delta))
		if index == -1 {
			m.focus = storeFinderFocus
		} else if index < count {
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
		if m.focus <= lastAction {
			m.focus = max(0, min(m.focusCount()-1, m.focus+delta))
			return m.syncFocus(), true
		}
		if delta < 0 && m.resultSelected == 0 {
			m.focus = 0
			return m.syncFocus(), true
		}
		return nil, false
	}
	if m.page == "upload" {
		first := 0
		if m.pendingFile != "" {
			first = 1 // The uploaded file is read-only.
		}
		m.focus = max(first, min(lastAction, m.focus+delta))
		return m.syncFocus(), true
	}
	_, form := managementForms[m.page]
	if m.page == "create" || form {
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
