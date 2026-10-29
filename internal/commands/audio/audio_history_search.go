package audio

import (
	"context"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/readable"
)

type audioHistorySearch struct {
	query   string
	focused bool
	matches []audiohistory.Entry
}

func (m *audioWorkbench) visibleHistory() []audiohistory.Entry {
	if m.historySearch.query == "" {
		return m.entries
	}
	return m.historySearch.matches
}

func audioHistoryClip(input string) string {
	clip := strings.Join(strings.Fields(readable.Text(input)), " ")
	if clip == "" {
		return "Generated speech"
	}
	return clip
}

func (m *audioWorkbench) appendHistoryQuery(text string) {
	text = strings.NewReplacer("\n", " ", "\t", " ").Replace(readable.Text(text))
	if text != "" {
		m.setHistoryQuery(m.historySearch.query + text)
	}
}

func (m *audioWorkbench) setHistoryQuery(query string) {
	var selected string
	visible := m.visibleHistory()
	if m.choice >= 0 && m.choice < len(visible) {
		selected = visible[m.choice].ID
	}
	search := &m.historySearch
	search.query, search.focused = query, true
	search.matches = search.matches[:0]
	m.choice = 0
	if query != "" {
		needle := strings.ToLower(query)
		for _, entry := range m.entries {
			if strings.Contains(strings.ToLower(entry.Settings.Voice), needle) ||
				strings.Contains(strings.ToLower(entry.Settings.Format), needle) ||
				strings.Contains(strings.ToLower(audioHistoryClip(entry.Settings.Input)), needle) {
				search.matches = append(search.matches, entry)
			}
		}
	}
	for i, entry := range m.visibleHistory() {
		if entry.ID == selected {
			m.choice = i
			break
		}
	}
}

func (m *audioWorkbench) updateHistory(key tea.KeyPressMsg) tea.Cmd {
	search := &m.historySearch
	visible := m.visibleHistory()
	switch key.String() {
	case "up", "shift+tab":
		if m.choice == 0 {
			search.focused = true
		} else if !search.focused {
			m.choice--
		}
	case "down", "tab":
		if len(visible) > 0 {
			if search.focused {
				search.focused = false
			} else {
				m.choice = min(m.choice+1, len(visible)-1)
			}
		}
	case "enter":
		if len(visible) > 0 {
			id := visible[m.choice].ID
			m.modal = ""
			m.stopPlayback()
			return m.work("open", func(ctx context.Context) audioWorkMessage { return m.load(ctx, id, "open") })
		}
	case "backspace", "ctrl+h":
		_, size := utf8.DecodeLastRuneInString(search.query)
		m.setHistoryQuery(search.query[:len(search.query)-size])
	case "ctrl+u":
		m.setHistoryQuery("")
	default:
		m.appendHistoryQuery(key.Text)
	}
	return nil
}

func (m *audioWorkbench) historySearchRow() (string, *tea.Cursor) {
	const prefix = "  Search  "
	query, cells := audioHistorySuffix(m.historySearch.query, m.audioWidth()-len(prefix)-2)
	if query == "" {
		query = m.muted().Render("Type to filter")
	}
	row := m.muted().Render(prefix) + query
	if !m.historySearch.focused {
		return row, nil
	}
	cursor := tea.NewCursor(len(prefix)+cells, 1)
	cursor.Shape, cursor.Blink = tea.CursorBar, true
	return row, cursor
}
