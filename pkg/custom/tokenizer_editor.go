package custom

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/tokenizer"
)

// The runner owns each worker. The editor sends immutable, revision-bound input.
type tokenizerEditorRequestMsg struct {
	Revision uint64
	Text     string
	Encoding string
}

type tokenizerEditorResultMsg struct {
	Revision uint64
	Tokens   []tokenizerPreviewToken
	Err      error
}

type tokenizerEditorStopMsg struct{ Code int }
type tokenizerEditorDebounceMsg struct{ revision uint64 }

type tokenizerEditor struct {
	text              string
	cursor            int
	boundaries        []int
	encoding          string
	invocation        string
	encodingChoice    int
	focus, tab, modal int
	selected, scroll  int
	tokenStart        int
	width, height     int
	color, dark       bool
	revision          uint64
	tokens            []tokenizerPreviewToken
	updating, failed  bool
	debouncing        bool
	note              string
	quit              bool
	exitCode          int
}

func newTokenizerEditor() *tokenizerEditor {
	return &tokenizerEditor{encoding: "o200k_base", invocation: "openai", boundaries: []int{0}, color: true, dark: true}
}

func (m *tokenizerEditor) Init() tea.Cmd { return nil }

func (m *tokenizerEditor) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.quit {
		return m, nil
	}
	switch msg := message.(type) {
	case tokenizerEditorStopMsg:
		m.quit, m.exitCode = true, msg.Code
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.keepSelectionVisible()
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
	case tokenizerInputPasteErrorMsg:
		m.note = msg.message
	case tokenizerEditorDebounceMsg:
		m.debouncing = false
		if msg.revision != m.revision && m.updating {
			return m, m.debounce()
		}
		if m.updating {
			request := tokenizerEditorRequestMsg{Revision: m.revision, Text: m.text, Encoding: m.encoding}
			return m, func() tea.Msg { return request }
		}
	case tokenizerEditorResultMsg:
		if msg.Revision != m.revision || !m.updating {
			return m, nil
		}
		m.updating = false
		if msg.Err != nil || !m.validTokens(msg.Tokens) {
			m.failed = true
			m.note = "Could not tokenize. Edit text or press r in results to retry."
			return m, nil
		}
		m.tokens, m.failed = msg.Tokens, false
		m.selected = max(0, min(m.selected, len(m.tokens)-1))
		m.keepSelectionVisible()
	case tea.PasteMsg:
		if m.focus == 0 && m.modal == 0 && m.usable() {
			return m, m.insert(msg.Content)
		}
		if m.usable() {
			m.note = "Tab to Text to paste."
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			m.quit, m.exitCode = true, 130
			return m, tea.Quit
		}
		if !m.usable() {
			return m, nil
		}
		if m.modal != 0 {
			m.updateModal(key)
			return m, nil
		}
		if key == "f1" || m.focus != 0 && key == "?" {
			m.modal, m.scroll = 1, 0
			return m, nil
		}
		switch key {
		case "tab", "shift+tab":
			direction := 1
			if key == "shift+tab" {
				direction = -1
			}
			m.focus = (m.focus + direction + 3) % 3
			m.encodingChoice = 0
			if m.encoding == "cl100k_base" {
				m.encodingChoice = 1
			}
			return m, nil
		case "esc":
			m.focus, m.note = 0, ""
			return m, nil
		}
		switch m.focus {
		case 0:
			return m, m.edit(msg)
		case 1:
			switch key {
			case "left":
				m.tab = (m.tab + 2) % 3
			case "right":
				m.tab = (m.tab + 1) % 3
			case "up":
				m.selected--
			case "down":
				m.selected++
			case "pgup":
				m.selected -= m.previousPageSize()
			case "pgdown":
				_, count := m.resultWindow(m.styles(), m.viewWidth())
				m.selected += max(1, count)
			case "home":
				m.selected = 0
			case "end":
				m.selected = len(m.tokens) - 1
			case "enter":
				if len(m.tokens) > 0 {
					m.modal, m.scroll = 2, 0
				}
			case "r":
				if m.failed {
					return m, m.changed()
				}
			}
			m.selected = max(0, min(m.selected, len(m.tokens)-1))
			m.keepSelectionVisible()
		case 2:
			switch key {
			case "up", "down", "left", "right":
				m.encodingChoice = 1 - m.encodingChoice
			case "enter":
				encoding := []string{"o200k_base", "cl100k_base"}[m.encodingChoice]
				if encoding != m.encoding {
					m.encoding = encoding
					return m, m.changed()
				}
			}
		}
	}
	return m, nil
}

func (m *tokenizerEditor) usable() bool { return m.width >= 40 && m.height >= 12 }

func (m *tokenizerEditor) validTokens(tokens []tokenizerPreviewToken) bool {
	end := uint32(0)
	for _, token := range tokens {
		if token.EndByte <= end || uint64(token.EndByte) > uint64(len(m.text)) {
			return false
		}
		end = token.EndByte
	}
	return int(end) == len(m.text)
}

func (m *tokenizerEditor) changed() tea.Cmd {
	m.revision++
	m.tokens, m.failed, m.note = nil, false, ""
	m.tokenStart = 0
	m.updating = m.text != ""
	if !m.updating {
		m.selected = 0
		return nil
	}
	return m.debounce()
}

func (m *tokenizerEditor) debounce() tea.Cmd {
	if m.debouncing {
		return nil
	}
	m.debouncing = true
	revision := m.revision
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tokenizerEditorDebounceMsg{revision: revision} })
}

func (m *tokenizerEditor) insert(text string) tea.Cmd {
	if !utf8.ValidString(text) {
		m.note = "Paste rejected: input must be valid UTF-8. Your text is unchanged."
		return nil
	}
	if len(text) > tokenizer.MaxInputBytes-len(m.text) {
		m.note = "Paste rejected: the input limit is 1 MiB. Your text is unchanged."
		return nil
	}
	if text == "" {
		return nil
	}
	m.text = m.text[:m.cursor] + text + m.text[m.cursor:]
	m.cursor += len(text)
	m.indexText()
	return m.changed()
}

// Source bytes stay exact. The display can escape controls without editing them.
func (m *tokenizerEditor) indexText() {
	m.boundaries = append(m.boundaries[:0], 0)
	for offset := 0; offset < len(m.text); {
		cluster, _ := ansi.FirstGraphemeCluster(m.text[offset:], ansi.WcWidth)
		offset += len(cluster)
		m.boundaries = append(m.boundaries, offset)
	}
	// Inserting a combining mark or ZWJ can join neighboring graphemes.
	for _, position := range m.boundaries {
		if position >= m.cursor {
			m.cursor = position
			break
		}
	}
}

func (m *tokenizerEditor) cursorIndex() int {
	return min(sort.SearchInts(m.boundaries, m.cursor), len(m.boundaries)-1)
}

func (m *tokenizerEditor) edit(msg tea.KeyPressMsg) tea.Cmd {
	index := m.cursorIndex()
	switch msg.String() {
	case "enter":
		return m.insert("\n")
	case "left":
		m.cursor = m.boundaries[max(0, index-1)]
	case "right":
		m.cursor = m.boundaries[min(len(m.boundaries)-1, index+1)]
	case "home", "ctrl+a":
		m.cursor = strings.LastIndexByte(m.text[:m.cursor], '\n') + 1
	case "end", "ctrl+e":
		m.cursor = m.lineEnd(m.cursor)
	case "up":
		m.moveLine(-1)
	case "down":
		m.moveLine(1)
	case "ctrl+u":
		if m.cursor > 0 {
			m.text, m.cursor = m.text[m.cursor:], 0
			m.indexText()
			return m.changed()
		}
	case "backspace":
		if index > 0 {
			start := m.boundaries[index-1]
			m.text, m.cursor = m.text[:start]+m.text[m.cursor:], start
			m.indexText()
			return m.changed()
		}
	case "delete":
		if index < len(m.boundaries)-1 {
			m.text = m.text[:m.cursor] + m.text[m.boundaries[index+1]:]
			m.indexText()
			return m.changed()
		}
	default:
		if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta) == 0 {
			return m.insert(msg.Text)
		}
	}
	return nil
}

func (m *tokenizerEditor) lineEnd(position int) int {
	end := len(m.text)
	if newline := strings.IndexByte(m.text[position:], '\n'); newline >= 0 {
		end = position + newline
		if end > 0 && m.text[end-1] == '\r' {
			end--
		}
	}
	return end
}

func (m *tokenizerEditor) moveLine(direction int) {
	start := strings.LastIndexByte(m.text[:m.cursor], '\n') + 1
	column := 0
	for _, boundary := range m.boundaries {
		if boundary >= start && boundary < m.cursor {
			column++
		}
	}
	if direction < 0 {
		if start == 0 {
			return
		}
		start = strings.LastIndexByte(m.text[:start-1], '\n') + 1
	} else {
		newline := strings.IndexByte(m.text[m.cursor:], '\n')
		if newline < 0 {
			return
		}
		start = m.cursor + newline + 1
	}
	end := m.lineEnd(start)
	m.cursor = start
	for _, boundary := range m.boundaries {
		if boundary > start && boundary <= end && column > 0 {
			m.cursor = boundary
			column--
		}
	}
}

func (m *tokenizerEditor) updateModal(key string) {
	_, total := m.modalRows(0, 0)
	m.scroll = min(m.scroll, max(0, total-max(1, m.viewHeight()-3)))
	switch key {
	case "esc", "f1":
		m.modal, m.scroll = 0, 0
	case "up":
		m.scroll--
	case "down":
		m.scroll++
	case "pgup":
		m.scroll -= max(1, m.viewHeight()-3)
	case "pgdown":
		m.scroll += max(1, m.viewHeight()-3)
	case "home":
		m.scroll = 0
	case "end":
		m.scroll = int(^uint(0) >> 1)
	}
	m.scroll = max(0, m.scroll)
}
