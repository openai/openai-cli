package custom

import (
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
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

const (
	tokenizerFocusText = iota
	tokenizerFocusOptions
	tokenizerFocusResults
)

const (
	tokenizerModalNone = iota
	tokenizerModalHelp
	tokenizerModalDetails
	tokenizerModalView
	tokenizerModalEncoding
)

type tokenizerEditor struct {
	text              string
	cursor            int
	boundaries        []int
	lineStarts        []int
	encoding          string
	invocation        string
	option, choice    int
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
	return &tokenizerEditor{encoding: tokenizer.DefaultEncoding, invocation: "openai", boundaries: []int{0}, lineStarts: []int{0}, color: true, dark: true}
}

func (m *tokenizerEditor) Init() tea.Cmd { return nil }

func (m *tokenizerEditor) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.quit {
		return m, nil
	}
	previousFocus, previousCursor := m.focus, m.cursor
	defer func() {
		if !m.quit && m.focus == tokenizerFocusText && (previousFocus != m.focus || previousCursor != m.cursor) {
			m.selectCursorToken()
		}
	}()
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
			m.tokens, m.failed = nil, true
			m.selected, m.tokenStart = 0, 0
			m.note = "Could not tokenize. Edit text or press r in Tokens to retry."
			return m, nil
		}
		m.tokens, m.failed = msg.Tokens, false
		if m.focus == tokenizerFocusText {
			m.selectCursorToken()
		} else {
			m.selected = max(0, min(m.selected, len(m.tokens)-1))
			m.keepSelectionVisible()
		}
	case tea.PasteMsg:
		if m.focus == tokenizerFocusText && m.modal == tokenizerModalNone && m.usable() {
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
		// Match the image picker when a quick Escape followed by typing is
		// decoded as an Alt key. No menu assigns Alt shortcuts.
		if msg.Mod&tea.ModAlt != 0 {
			printable := (msg.Mod == tea.ModAlt || msg.Mod == tea.ModAlt|tea.ModShift) && unicode.IsPrint(msg.Code)
			if m.focus != tokenizerFocusText || printable || m.modal != tokenizerModalNone {
				m.focus, m.modal, m.option, m.note = tokenizerFocusText, tokenizerModalNone, 0, ""
				if printable {
					letter := msg.Code
					if msg.Mod&tea.ModShift != 0 {
						letter = unicode.ToUpper(letter)
					}
					return m, m.insert(string(letter))
				}
				return m, nil
			}
		}
		if m.modal == tokenizerModalView || m.modal == tokenizerModalEncoding {
			return m, m.updateChoice(key)
		}
		if m.modal != tokenizerModalNone {
			m.updateModal(key)
			return m, nil
		}
		if key == "f1" || m.focus != tokenizerFocusText && key == "?" {
			m.modal, m.scroll = tokenizerModalHelp, 0
			return m, nil
		}
		switch key {
		case "tab", "shift+tab":
			direction := 1
			if key == "shift+tab" {
				direction = -1
			}
			m.focus = (m.focus + direction + 3) % 3
			return m, nil
		case "esc":
			m.focus, m.option, m.note = tokenizerFocusText, 0, ""
			return m, nil
		}
		switch m.focus {
		case tokenizerFocusText:
			if key == "down" && m.lineEnd(m.cursor) == len(m.text) {
				m.focus, m.option = tokenizerFocusOptions, 0
				return m, nil
			}
			return m, m.edit(msg)
		case tokenizerFocusOptions:
			switch key {
			case "up":
				if m.option == 0 {
					m.focus = tokenizerFocusText
				} else {
					m.option--
				}
			case "down":
				if m.option == 1 {
					m.focus = tokenizerFocusResults
				} else {
					m.option++
				}
			case "home":
				m.option = 0
			case "end":
				m.option = 1
			case "left", "right":
				if m.option == 0 {
					direction := 1
					if key == "left" {
						direction = -1
					}
					m.tab = (m.tab + direction + 3) % 3
					m.keepSelectionVisible()
				} else if key == "left" {
					m.focus = tokenizerFocusText
				} else {
					m.openChoice()
				}
			case "enter":
				m.openChoice()
			}
		case tokenizerFocusResults:
			switch key {
			case "up":
				m.focus, m.option = tokenizerFocusOptions, 1
			case "left":
				m.selected--
			case "right":
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
					m.modal, m.scroll = tokenizerModalDetails, 0
				}
			case "r":
				if m.failed {
					return m, m.changed()
				}
			}
			m.selected = max(0, min(m.selected, len(m.tokens)-1))
			m.keepSelectionVisible()
		}
	}
	return m, nil
}

func (m *tokenizerEditor) openChoice() {
	m.modal, m.choice = tokenizerModalView, m.tab
	if m.option == 1 {
		m.modal, m.choice = tokenizerModalEncoding, 0
		for index, encoding := range tokenizer.SupportedEncodings() {
			if m.encoding == encoding {
				m.choice = index
				break
			}
		}
	}
}

func (m *tokenizerEditor) updateChoice(key string) tea.Cmd {
	last := 2
	var encodings []string
	if m.modal == tokenizerModalEncoding {
		encodings = tokenizer.SupportedEncodings()
		last = len(encodings) - 1
	}
	switch key {
	case "up":
		m.choice = max(0, m.choice-1)
	case "down":
		m.choice = min(last, m.choice+1)
	case "home":
		m.choice = 0
	case "end":
		m.choice = last
	case "esc":
		m.modal, m.focus, m.option = tokenizerModalNone, tokenizerFocusText, 0
	case "left", "f1":
		m.modal = tokenizerModalNone
	case "tab", "shift+tab":
		m.modal, m.focus = tokenizerModalNone, tokenizerFocusResults
		if key == "shift+tab" {
			m.focus = tokenizerFocusText
		}
	case "enter":
		modal := m.modal
		m.modal = tokenizerModalNone
		if modal == tokenizerModalView {
			m.tab = m.choice
			m.keepSelectionVisible()
		} else {
			encoding := encodings[m.choice]
			if encoding != m.encoding {
				m.encoding = encoding
				return m.changed()
			}
		}
	}
	return nil
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
	m.selected, m.tokenStart = 0, 0
	m.updating = m.text != ""
	if !m.updating {
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
	start := m.cursor
	m.text = m.text[:start] + text + m.text[start:]
	m.cursor += len(text)
	m.reindexText(start)
	return m.changed()
}

// Source bytes stay exact. The display can escape controls without editing them.
func (m *tokenizerEditor) indexText() {
	m.reindexText(0)
}

func (m *tokenizerEditor) reindexText(editedAt int) {
	// The preceding whole grapheme contains any context that can join the edit.
	// Keep its unchanged prefix, then segment through EOF without reconvergence.
	index, offset := 0, 0
	if editedAt > 0 && len(m.boundaries) > 0 {
		index = max(0, sort.SearchInts(m.boundaries, editedAt)-1)
		offset = m.boundaries[index]
	}
	m.boundaries = append(m.boundaries[:index], offset)
	keepLines := sort.Search(len(m.lineStarts), func(i int) bool { return m.lineStarts[i] > offset })
	m.lineStarts = m.lineStarts[:keepLines]
	if len(m.lineStarts) == 0 {
		m.lineStarts = append(m.lineStarts, 0)
	}
	suffix := m.text[offset:]
	if len(suffix) > cap(m.boundaries)-len(m.boundaries) && utf8.RuneCountInString(suffix) == len(suffix) {
		// ASCII has at most one grapheme per byte. Reserve once for large pastes.
		m.boundaries = slices.Grow(m.boundaries, len(suffix))
	}
	m.lineStarts = slices.Grow(m.lineStarts, strings.Count(suffix, "\n"))
	for offset < len(m.text) {
		cluster, _ := ansi.FirstGraphemeCluster(m.text[offset:], ansi.WcWidth)
		offset += len(cluster)
		m.boundaries = append(m.boundaries, offset)
		if strings.HasSuffix(cluster, "\n") {
			m.lineStarts = append(m.lineStarts, offset)
		}
	}
	// Inserting a combining mark or ZWJ can join neighboring graphemes.
	m.cursor = m.boundaries[min(sort.SearchInts(m.boundaries, m.cursor), len(m.boundaries)-1)]
}

func (m *tokenizerEditor) cursorIndex() int {
	return min(sort.SearchInts(m.boundaries, m.cursor), len(m.boundaries)-1)
}

// A caret selects the token containing its byte position: [start, end).
// Boundaries select the following token; EOF selects the final token fragment.
// Grapheme movement stays unchanged even when tokens split a UTF-8 character.
func (m *tokenizerEditor) selectCursorToken() {
	if m.updating || m.failed || len(m.tokens) == 0 {
		m.selected, m.tokenStart = 0, 0
		return
	}
	m.selected = sort.Search(len(m.tokens), func(index int) bool {
		return int(m.tokens[index].EndByte) > m.cursor
	})
	m.selected = min(m.selected, len(m.tokens)-1)
	m.keepSelectionVisible()
}

func (m *tokenizerEditor) edit(msg tea.KeyPressMsg) tea.Cmd {
	index := m.cursorIndex()
	switch msg.String() {
	case "enter", "alt+enter":
		return m.insert("\n")
	case "left":
		m.cursor = m.boundaries[max(0, index-1)]
	case "right":
		m.cursor = m.boundaries[min(len(m.boundaries)-1, index+1)]
	case "home", "ctrl+a":
		m.cursor = m.lineStarts[m.lineIndex(m.cursor)]
	case "end", "ctrl+e":
		m.cursor = m.lineEnd(m.cursor)
	case "ctrl+home":
		m.cursor = 0
	case "ctrl+end":
		m.cursor = len(m.text)
	case "ctrl+left":
		m.moveWord(-1)
	case "ctrl+right":
		m.moveWord(1)
	case "up":
		m.moveLine(-1)
	case "down":
		m.moveLine(1)
	case "pgup":
		m.moveLine(-m.sourceRows())
	case "pgdown":
		m.moveLine(m.sourceRows())
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
			m.reindexText(start)
			return m.changed()
		}
	case "delete":
		if index < len(m.boundaries)-1 {
			m.text = m.text[:m.cursor] + m.text[m.boundaries[index+1]:]
			m.reindexText(m.cursor)
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
	if line := m.lineIndex(position); line+1 < len(m.lineStarts) {
		end = m.lineStarts[line+1] - 1
		if end > 0 && m.text[end-1] == '\r' {
			end--
		}
	}
	return end
}

func (m *tokenizerEditor) lineIndex(position int) int {
	return max(0, sort.Search(len(m.lineStarts), func(i int) bool { return m.lineStarts[i] > position })-1)
}

func (m *tokenizerEditor) moveLine(direction int) {
	line := m.lineIndex(m.cursor)
	target := max(0, min(line+direction, len(m.lineStarts)-1))
	if target == line {
		return
	}
	column := m.cursorIndex() - sort.SearchInts(m.boundaries, m.lineStarts[line])
	start := m.lineStarts[target]
	first := sort.SearchInts(m.boundaries, start)
	last := sort.SearchInts(m.boundaries, m.lineEnd(start))
	m.cursor = m.boundaries[min(first+column, last)]
}

func (m *tokenizerEditor) moveWord(direction int) {
	index := m.cursorIndex()
	space := func(at int) bool {
		value, _ := utf8.DecodeRuneInString(m.text[m.boundaries[at]:])
		return unicode.IsSpace(value)
	}
	if direction < 0 {
		for index > 0 && space(index-1) {
			index--
		}
		for index > 0 && !space(index-1) {
			index--
		}
	} else {
		for index+1 < len(m.boundaries) && !space(index) {
			index++
		}
		for index+1 < len(m.boundaries) && space(index) {
			index++
		}
	}
	m.cursor = m.boundaries[index]
}

func (m *tokenizerEditor) updateModal(key string) {
	_, total := m.modalRows(0, 0)
	count := m.modalPageSize(total)
	m.scroll = min(m.scroll, max(0, total-count))
	switch key {
	case "esc", "left", "f1":
		m.modal, m.scroll = 0, 0
	case "up":
		m.scroll--
	case "down":
		m.scroll++
	case "pgup":
		m.scroll -= count
	case "pgdown":
		m.scroll += count
	case "home":
		m.scroll = 0
	case "end":
		m.scroll = int(^uint(0) >> 1)
	}
	m.scroll = max(0, m.scroll)
}
