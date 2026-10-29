package batches

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

var batchExpirationPresets = []batchBrowserChoice{
	{value: "0", title: "Server default"}, {value: "3600", title: "1 hour"},
	{value: "86400", title: "1 day"}, {value: "604800", title: "7 days"},
	{value: "2592000", title: "30 days"},
	{value: "custom", title: "Custom duration", detail: "Enter seconds from 3600 (1 hour) to 2592000 (30 days)."},
}

func (m *batchBrowserScreen) updateSettingLabels() {
	d := m.flow.draft
	for i := range m.choices {
		row := &m.choices[i]
		switch row.value {
		case "metadata":
			row.setting = d.metadata
			if strings.TrimSpace(row.setting) == "" {
				row.setting = "None"
			}
		case "expires":
			row.setting = fmt.Sprintf("%d seconds", d.expires)
			for _, preset := range batchExpirationPresets {
				if preset.value == strconv.FormatInt(d.expires, 10) {
					row.setting = preset.title
					break
				}
			}
		}
	}
}

func (m *batchBrowserScreen) restoreFilePrompt() {
	m.input.Reset()
	m.input.Prompt = "Input file    "
	m.input.Placeholder = batchInputLabel(m.flow.draft.sourceLabel())
}

func (m *batchBrowserScreen) flowEditingRow() bool {
	return m.flowInputRow() || m.flow.setting == "metadata" || m.flow.setting == "custom"
}

func (m *batchBrowserScreen) setExpirationOpen(open bool) {
	parent := slices.IndexFunc(m.choices, func(c batchBrowserChoice) bool { return c.value == "expires" })
	// Expiration is the last setting on the form. Its choices only exist while expanded.
	m.choices = m.choices[:parent+1]
	m.flow.setting = ""
	selected := parent
	if open {
		m.flow.setting = "expires"
		current := strconv.FormatInt(m.flow.draft.expires, 10)
		for _, preset := range batchExpirationPresets {
			if preset.value == current || preset.value == "custom" && selected == parent {
				selected = len(m.choices)
			}
			preset.title = "  " + preset.title
			m.choices = append(m.choices, preset)
		}
	}
	m.filter()
	m.index = selected
}

func (m *batchBrowserScreen) beginSetting(field, value string) {
	m.flow.setting = field
	m.input.Prompt, m.input.Placeholder = "Metadata  ", `{"key":"value"}`
	if field == "custom" {
		m.input.Prompt, m.input.Placeholder = "  Custom duration  ", "Seconds"
	}
	m.input.SetValue(value)
	m.notice = ""
}

func (m *batchBrowserScreen) applySetting() error {
	value := m.input.Value()
	if m.flow.setting == "metadata" {
		if _, err := parseBatchBrowserMetadata(value); err != nil {
			return err
		}
		m.flow.draft.metadata = value
	} else {
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || n != 0 && (n < 3600 || n > 2592000) {
			return batchError("Enter seconds from 3600 to 2592000, or choose Server default.")
		}
		m.flow.draft.expires = n
		m.setExpirationOpen(false)
	}
	m.flow.setting = ""
	m.updateSettingLabels()
	return nil
}

func (m *batchBrowserScreen) updateCreateSetting(message tea.Msg) (tea.Cmd, bool) {
	key, isKey := message.(tea.KeyPressMsg)
	if len(m.visible) == 0 || isKey && key.String() == "ctrl+c" {
		return nil, false
	}
	choice := m.choices[m.visible[m.index]]
	_, paste := message.(tea.PasteMsg)
	typing := isKey && key.Mod & ^tea.ModShift == 0 && strings.IndexFunc(key.Text, unicode.IsPrint) >= 0
	if choice.value == "metadata" && m.flow.setting != "metadata" && (paste || typing) {
		// Typing replaces a field. Enter opens its current value for editing.
		m.beginSetting("metadata", "")
	}
	if m.flow.setting == "metadata" || m.flow.setting == "custom" {
		if isKey {
			switch key.String() {
			case "enter":
				if m.width < 30 || m.height < 7 {
					return nil, true
				}
				if err := m.applySetting(); err != nil {
					m.notice = err.Error()
					return nil, true
				}
				m.restoreFilePrompt()
				m.notice = ""
				return nil, true
			case "esc":
				if m.flow.setting == "custom" {
					m.flow.setting = "expires"
				} else {
					m.flow.setting = ""
				}
				m.restoreFilePrompt()
				m.notice = ""
				return nil, true
			case "up", "down", "tab", "shift+tab", "pgup", "pgdown":
				return nil, true
			}
		}
		previous := m.input.Value()
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		if previous != m.input.Value() {
			m.notice = ""
		}
		return command, true
	}
	if !isKey {
		return nil, false
	}
	if m.flow.setting == "expires" {
		switch key.String() {
		case "esc", "left":
			m.setExpirationOpen(false)
			return nil, true
		case "up", "shift+tab", "pgup":
			if m.choices[m.visible[m.index]].value == "expires" {
				m.setExpirationOpen(false)
			}
		}
	}
	if key.String() != "enter" || m.width < 30 || m.height < 7 {
		return nil, false
	}
	switch choice.value {
	case "metadata":
		m.beginSetting("metadata", m.flow.draft.metadata)
	case "expires":
		m.setExpirationOpen(m.flow.setting != "expires")
	case "custom":
		value := ""
		if m.flow.draft.expires > 0 {
			value = strconv.FormatInt(m.flow.draft.expires, 10)
		}
		m.beginSetting("custom", value)
	default:
		if m.flow.setting != "expires" {
			return nil, false
		}
		n, err := strconv.ParseInt(choice.value, 10, 64)
		if err != nil {
			return nil, false
		}
		m.flow.draft.expires = n
		m.setExpirationOpen(false)
		m.updateSettingLabels()
	}
	m.notice = ""
	return nil, true
}
