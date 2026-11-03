package audio

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/readable"
)

type audioHistorySearch struct {
	query      string
	focused    bool
	showFolder bool
	day        time.Time
	matches    []audiohistory.Entry
}

func (m *audioWorkbench) visibleHistory(now time.Time) []audiohistory.Entry {
	if m.historySearch.query == "" {
		return m.entries
	}
	if m.historySearch.day != audioHistoryDay(now) {
		m.filterHistory(m.historySearch.query, now)
	}
	return m.historySearch.matches
}

func audioHistoryDay(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func audioHistoryYesterday(now time.Time) string {
	// Use local calendar fields in UTC because some time zones skip midnight.
	return time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
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
	m.filterHistory(query, time.Now())
	m.historySearch.focused = true
}

func (m *audioWorkbench) filterHistory(query string, now time.Time) {
	search := &m.historySearch
	var selected string
	visible := m.entries
	if search.query != "" {
		visible = search.matches
	}
	if m.choice >= 0 && m.choice < len(visible) {
		selected = visible[m.choice].ID
	}
	search.query, search.day = query, audioHistoryDay(now)
	search.matches = search.matches[:0]
	visible = m.entries
	m.choice = 0
	if query != "" {
		filter := parseHistoryFilter(query, now)
		for _, entry := range m.entries {
			if filter.matches(entry, now) {
				search.matches = append(search.matches, entry)
			}
		}
		visible = search.matches
	}
	for i, entry := range visible {
		if entry.ID == selected {
			m.choice = i
			break
		}
	}
}

type audioHistoryFilter struct {
	terms  []string
	date   string
	layout string
	phrase string
}

// Match calendar dates before searching the remaining words in the clip, voice, and format.
func parseHistoryFilter(query string, now time.Time) audioHistoryFilter {
	query = strings.ToLower(query)
	words := strings.Fields(query)
	for i, word := range words {
		date, layout, used := "", time.DateOnly, 1
		switch word {
		case "today":
			date = now.Format(layout)
		case "yesterday":
			date = audioHistoryYesterday(now)
		default:
			for _, spec := range []struct {
				words int
				parse string
				match string
			}{
				{1, time.DateOnly, time.DateOnly},
				{3, "Jan 2 2006", time.DateOnly}, {3, "January 2 2006", time.DateOnly},
				{2, "Jan 2", "Jan 2"}, {2, "January 2", "Jan 2"},
				{2, "Jan 2006", "2006-01"}, {2, "January 2006", "2006-01"},
				{1, "2006-01", "2006-01"}, {1, "2006", "2006"},
				{1, "Jan", "01"}, {1, "January", "01"},
			} {
				if i+spec.words > len(words) {
					continue
				}
				input := strings.Join(words[i:i+spec.words], " ")
				if spec.words == 3 {
					input = strings.Replace(input, ", ", " ", 1)
				}
				if day, err := time.Parse(spec.parse, input); err == nil {
					date, layout, used = day.Format(spec.match), spec.match, spec.words
					break
				}
			}
		}
		if date != "" {
			filter := audioHistoryFilter{terms: append(words[:i], words[i+used:]...), date: date, layout: layout}
			if len(filter.terms) > 0 {
				// A date can also be part of the recorded text, as in "meet me today".
				filter.phrase = query
			}
			return filter
		}
	}
	// A text-only query retains phrase matching, including spaces between words.
	return audioHistoryFilter{terms: []string{query}}
}

func (f audioHistoryFilter) matches(entry audiohistory.Entry, now time.Time) bool {
	local := entry.CreatedAt.In(now.Location())
	if f.date != "" && (entry.CreatedAt.IsZero() || local.Format(f.layout) != f.date) {
		return f.phrase != "" && strings.Contains(strings.ToLower(audioHistoryClip(entry.Settings.Input)), f.phrase)
	}
	if len(f.terms) == 0 {
		return true
	}
	voice, format := strings.ToLower(entry.Settings.Voice), strings.ToLower(entry.Settings.Format)
	clip := strings.ToLower(audioHistoryClip(entry.Settings.Input))
	for _, term := range f.terms {
		if !strings.Contains(clip, term) && !strings.Contains(voice, term) && !strings.Contains(format, term) {
			return false
		}
	}
	return true
}

func (m *audioWorkbench) updateHistory(key tea.KeyPressMsg) tea.Cmd {
	search := &m.historySearch
	visible := m.visibleHistory(time.Now())
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
	case "ctrl+l":
		search.showFolder = !search.showFolder
	default:
		m.appendHistoryQuery(key.Text)
	}
	return nil
}

func (m *audioWorkbench) historySearchRow() (string, *tea.Cursor) {
	const prefix = "  Search  "
	query, cells := audioHistorySuffix(m.historySearch.query, m.audioWidth()-len(prefix)-2)
	label := m.muted()
	if m.historySearch.focused {
		label = m.accent()
	}
	if query == "" {
		hint := "Clip, voice, or date"
		if m.audioWidth() >= 52 {
			hint = "Clip, voice, format, or date"
		}
		if m.audioWidth() >= 78 {
			hint += "   Try today, Oct 28, or YYYY-MM-DD"
		}
		query = m.muted().Render(hint)
	} else {
		query = m.primaryText().Render(query)
	}
	row := label.Render(prefix) + query
	if !m.historySearch.focused {
		return row, nil
	}
	cursor := tea.NewCursor(len(prefix)+cells, 1)
	cursor.Shape, cursor.Blink = tea.CursorBar, true
	return row, cursor
}
