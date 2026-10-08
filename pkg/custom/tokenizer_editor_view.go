package custom

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *tokenizerEditor) viewHeight() int { return min(18, max(0, m.height-1)) }
func (m *tokenizerEditor) viewWidth() int  { return max(1, min(100, m.width-4)) }
func (m *tokenizerEditor) roomy() bool     { return m.width >= 50 && m.viewHeight() >= 16 }
func (m *tokenizerEditor) resultRows() int {
	if m.roomy() {
		return 2
	}
	return 1
}

type tokenizerEditorStyles struct {
	title, muted, accent, selected, focused, border, input lipgloss.Style
}

func (m *tokenizerEditor) styles() tokenizerEditorStyles {
	s := tokenizerEditorStyles{}
	if !m.color {
		return s
	}
	focus, fill, text, muted, border := "#3159BC", "#E9EFFE", "#20283B", "#657087", "#BAC4D8"
	if m.dark {
		focus, fill, text, muted, border = "#8AA8FF", "#343D58", "#F2F4FA", "#A4ACC2", "#51566B"
	}
	s.title = s.title.Bold(true)
	s.muted = s.muted.Foreground(lipgloss.Color(muted))
	s.accent = s.accent.Foreground(lipgloss.Color(focus)).Bold(true)
	s.selected = s.selected.Foreground(lipgloss.Color(text)).Background(lipgloss.Color(fill))
	s.focused = s.selected.Bold(true)
	s.border = s.border.Foreground(lipgloss.Color(border))
	if m.focus == tokenizerFocusText {
		s.border = s.border.Foreground(lipgloss.Color(focus))
		if m.text != "" {
			s.input = s.selected
		}
	}
	return s
}

func (m *tokenizerEditor) View() tea.View {
	view := tea.NewView("")
	if m.quit || m.width <= 0 || m.height <= 0 {
		return view
	}
	if !m.usable() {
		lines := []string{"Resize to 40 x 12.", "Ctrl+C exit"}
		plain := "Plain: " + readable.Text(m.invocation) + " tokenizer --format text"
		if ansi.StringWidth(plain) <= m.width && m.height >= 3 {
			lines = append(lines, plain)
		}
		view.Content = m.fit(lines, false)
		return view
	}
	s := m.styles()
	if m.modal == tokenizerModalView || m.modal == tokenizerModalEncoding {
		view.Content = m.choiceView(s)
		return view
	}
	if m.modal != 0 {
		view.Content = m.modalView(s)
		return view
	}
	width := m.viewWidth()
	lines := []string{s.title.Render("Tokenizer")}
	if m.roomy() {
		caption := "╭─ Text "
		lines = append(lines, s.border.Render(caption+strings.Repeat("─", width-ansi.StringWidth(caption)-1)+"╮"))
		for _, line := range m.editorLines(width-4, 3) {
			input := s.input
			if line == "" || line == "▏" {
				input = lipgloss.NewStyle()
			}
			line = imagePickerPromptFit(line, width-4)
			// Match the image prompt's explicit edge placement. Terminals can
			// disagree about the cell width of complete emoji clusters.
			row := s.border.Render("│") + input.Render(ansi.EraseCharacter(width-2)+" "+line+" ") +
				ansi.CursorHorizontalAbsolute(width+2) + s.border.Render("│")
			lines = append(lines, row)
		}
		lines = append(lines, s.border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	} else {
		prefix := "  Text "
		if m.focus == tokenizerFocusText {
			prefix = "› Text "
		}
		caption := s.muted
		if m.focus == tokenizerFocusText {
			caption = s.accent
		}
		lines = append(lines, caption.Render(prefix)+m.editorLines(width-ansi.StringWidth(prefix), 1)[0])
	}
	if m.note == "" {
		lines = append(lines, "")
	}
	tokenLabel := "tokens"
	if len(m.tokens) == 1 {
		tokenLabel = "token"
	}
	status := fmt.Sprintf("%d %s", len(m.tokens), tokenLabel)
	if m.updating {
		status = "Updating…"
	} else if m.failed {
		status = "Count unavailable"
	}
	if m.tab == 2 {
		byteLabel := "bytes"
		if len(m.text) == 1 {
			byteLabel = "byte"
		}
		status += fmt.Sprintf(" · %d %s", len(m.text), byteLabel)
	}
	lines = append(lines, "  "+status)
	viewActive := m.focus == tokenizerFocusOptions && m.option == 0
	var tabs []string
	for i, label := range []string{"Text", "Token IDs", "Bytes"} {
		if m.tab == i {
			label = "[" + label + "]"
			if viewActive {
				label = s.focused.Render(label)
			}
		}
		tabs = append(tabs, label)
	}
	views := m.optionRow(s, "View", strings.Join(tabs, " "), viewActive)
	if ansi.StringWidth(views+"  ←→") <= width {
		views += s.muted.Render("  ←→")
	}
	lines = append(lines, views)
	encodingActive := m.focus == tokenizerFocusOptions && m.option == 1
	encoding := m.encoding
	if encodingActive {
		encoding = s.focused.Render(encoding)
	}
	lines = append(lines, m.optionRow(s, "Tokenizer", encoding+s.muted.Render("  ›"), encodingActive))
	if m.note == "" {
		lines = append(lines, "")
	}
	position := "Tokens"
	if len(m.tokens) > 0 {
		position = fmt.Sprintf("Token %d of %d", m.selected+1, len(m.tokens))
		fragment, _, _ := m.fragment(m.selected)
		if !utf8.ValidString(fragment) {
			position += " · partial UTF-8"
		}
	}
	if m.focus == tokenizerFocusResults && len(m.tokens) == 0 {
		lines = append(lines, s.accent.Render("› "+position))
	} else {
		caption := s.muted
		if m.focus == tokenizerFocusResults {
			caption = s.accent
		}
		lines = append(lines, "  "+caption.Render(position))
	}
	lines = append(lines, m.resultLines(s, width)...)
	if m.note != "" {
		notes := strings.Split(ansi.Wrap(m.note, width, ""), "\n")
		available := max(0, m.viewHeight()-len(lines)-1)
		lines = append(lines, notes[:min(len(notes), available)]...)
	}
	if m.note == "" && lines[len(lines)-1] != "" && len(lines)+1 < m.viewHeight() {
		lines = append(lines, "")
	}
	lines = append(lines, s.muted.Render(m.footer()))
	view.Content = m.fit(lines, true)
	return view
}

func (m *tokenizerEditor) optionRow(s tokenizerEditorStyles, label, value string, active bool) string {
	marker, caption := "  ", s.muted
	if active {
		marker, caption = s.accent.Render("› "), s.accent
	}
	return marker + caption.Render(fmt.Sprintf("%-11s", label)) + value
}

func (m *tokenizerEditor) highlightRow(s tokenizerEditorStyles, value string, active bool) string {
	value = tokenizerClip(value, m.viewWidth()-2)
	if active {
		return s.accent.Render("› ") + s.selected.Render(value+strings.Repeat(" ", max(0, m.viewWidth()-2-ansi.StringWidth(value))))
	}
	return "  " + value
}

func (m *tokenizerEditor) footer() string {
	footer := "Ctrl+C exit · ↑↓ move · Enter select"
	switch m.focus {
	case tokenizerFocusText:
		footer = "Ctrl+C exit · Tab options · Enter newline"
		if ansi.StringWidth(footer) > m.viewWidth() {
			footer = "Ctrl+C exit · Tab options"
		}
	case tokenizerFocusResults:
		footer = "Ctrl+C exit · ←→ token · Enter details"
		if ansi.StringWidth(footer) > m.viewWidth() {
			footer = "Ctrl+C exit · ←→ · Enter details"
		}
	case tokenizerFocusOptions:
		if m.option == 0 {
			footer = "Ctrl+C exit · ←→ view · Enter select"
			if ansi.StringWidth(footer+" · ↑↓ move") <= m.viewWidth() {
				footer += " · ↑↓ move"
			}
		}
	}
	if m.focus != tokenizerFocusText && ansi.StringWidth(footer+" · Tab switch") <= m.viewWidth() {
		footer += " · Tab switch"
	}
	if ansi.StringWidth(footer+" · F1 help") <= m.viewWidth() {
		footer += " · F1 help"
	}
	return footer
}

func (m *tokenizerEditor) choiceView(s tokenizerEditorStyles) string {
	title, explanation := "Choose view", "Show the same tokens in a different form."
	labels := []string{"Text", "Token IDs", "Bytes"}
	descriptions := []string{"Readable pieces", "Numeric token IDs", "Exact hex bytes"}
	current := m.tab
	if m.modal == tokenizerModalEncoding {
		title, explanation = "Choose tokenizer", "The tokenizer sets how text is split into tokens."
		labels = []string{"o200k_base", "cl100k_base"}
		descriptions = []string{"Default", "Alternate vocabulary"}
		current = 0
		if m.encoding == "cl100k_base" {
			current = 1
		}
	}
	lines := []string{s.title.Render(title)}
	lines = append(lines, strings.Split(ansi.Wrap(explanation, m.viewWidth(), ""), "\n")...)
	lines = append(lines, "")
	for i, label := range labels {
		if i == current {
			label += " ✓"
		}
		line := fmt.Sprintf("%-14s%s", label, descriptions[i])
		if ansi.StringWidth(line) > m.viewWidth()-2 {
			line = label
		}
		lines = append(lines, m.highlightRow(s, line, i == m.choice))
	}
	lines = append(lines, "", s.muted.Render("↑↓ choose · Enter use · Esc cancel"), s.muted.Render("Ctrl+C exit"))
	return m.fit(lines, true)
}

func (m *tokenizerEditor) fit(lines []string, inset bool) string {
	height, width := m.viewHeight(), m.viewWidth()
	if !inset {
		height, width = m.height, m.width
	}
	lines = lines[:min(len(lines), max(0, height))]
	for i, line := range lines {
		lines[i] = tokenizerClip(line, max(1, width))
		if inset {
			lines[i] = "  " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// Escape presentation controls without normalizing source bytes or ZWJ clusters.
var tokenizerSourceControls = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`)

func tokenizerSourceDisplay(value string) string {
	if len(value) > 128 {
		return fmt.Sprintf("<%d-byte cluster>", len(value))
	}
	display := tokenizerSourceControls.Replace(readable.Text(value))
	if ansi.StringWidth(display) == 0 {
		quoted := strconv.QuoteToASCII(value)
		return quoted[1 : len(quoted)-1]
	}
	return display
}

func (m *tokenizerEditor) editorLines(width, count int) []string {
	start := strings.LastIndexByte(m.text[:m.cursor], '\n') + 1
	if count > 1 && start > 0 {
		start = strings.LastIndexByte(m.text[:start-1], '\n') + 1
	}
	lines := make([]string, 0, count)
	for len(lines) < count {
		end := len(m.text)
		if next := strings.IndexByte(m.text[start:], '\n'); next >= 0 {
			end = start + next
		}
		cursor := -1
		if m.cursor >= start && m.cursor <= end && m.focus == 0 {
			cursor = m.cursor
		}
		line := m.editorLine(start, end, cursor, width)
		if m.text == "" && len(lines) == 0 {
			line = "Type or paste text…"
			if m.focus == 0 {
				line = "▏ " + line
			}
		}
		lines = append(lines, tokenizerClip(line, width))
		if end == len(m.text) {
			break
		}
		start = end + 1
	}
	for len(lines) < count {
		lines = append(lines, "")
	}
	return lines
}

func (m *tokenizerEditor) editorLine(start, end, cursor, width int) string {
	// Bound work by visible graphemes, using the index built when text changes.
	// A cursor near the end of a large line must not scan its complete prefix.
	first := sort.SearchInts(m.boundaries, start)
	index := first
	if cursor >= 0 {
		index = sort.SearchInts(m.boundaries, cursor)
		used := 0
		for index > first {
			part := m.text[m.boundaries[index-1]:min(m.boundaries[index], end)]
			cells := tokenizerSourceWidth(tokenizerSourceDisplay(part))
			if used+cells > max(1, width-3) {
				break
			}
			used += cells
			index--
		}
	}
	var out strings.Builder
	if index > first {
		out.WriteRune('…')
	}
	used := tokenizerSourceWidth(out.String())
	for index < len(m.boundaries) && m.boundaries[index] <= end {
		offset := m.boundaries[index]
		if offset == cursor {
			out.WriteRune('▏')
			used++
		}
		if offset == end || index == len(m.boundaries)-1 {
			break
		}
		part := m.text[offset:min(m.boundaries[index+1], end)]
		display := tokenizerSourceDisplay(part)
		cells := tokenizerSourceWidth(display)
		if used+cells > width-1 {
			out.WriteRune('…')
			break
		}
		out.WriteString(display)
		used += cells
		index++
	}
	return out.String()
}

// Source windows and padding must share the inline painter's conservative
// grapheme widths, including terminals that render a ZWJ cluster as scalars.
func tokenizerSourceWidth(text string) int {
	width := 0
	for len(text) > 0 {
		_, cells, read := imagePickerSequence(text)
		width += cells
		text = text[read:]
	}
	return width
}

func (m *tokenizerEditor) fragment(index int) (string, int, int) {
	start := 0
	if index > 0 {
		start = int(m.tokens[index-1].EndByte)
	}
	end := int(m.tokens[index].EndByte)
	return m.text[start:end], start, end
}

func tokenizerFragmentLabel(fragment string, width int) string {
	if !utf8.ValidString(fragment) {
		bytes := []byte(fragment[:min(len(fragment), max(1, (width-2)/2))])
		label := "0x" + hex.EncodeToString(bytes)
		if len(bytes) < len(fragment) {
			label += "…"
		}
		return tokenizerClip(label, width)
	}
	prefix := fragment[:min(len(fragment), max(1, width)*4)]
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	label := strconv.QuoteToGraphic(prefix)
	if len(prefix) != len(fragment) {
		label += "…"
	}
	return tokenizerClip(label, width)
}

func (m *tokenizerEditor) resultLines(s tokenizerEditorStyles, width int) []string {
	lines, _ := m.resultWindow(s, width)
	return lines
}

func (m *tokenizerEditor) resultWindow(s tokenizerEditorStyles, width int) ([]string, int) {
	rows := m.resultRows()
	if len(m.tokens) == 0 {
		label := "Type or paste text above."
		if m.updating {
			label = "Calculating exact tokens…"
		} else if m.failed {
			label = "Edit text or press r here to retry."
		}
		lines := []string{"  " + s.muted.Render(tokenizerClip(label, width-2))}
		for len(lines) < rows {
			lines = append(lines, "")
		}
		return lines, 0
	}
	start := m.resultStart(width)
	var lines []string
	line, used := " ", 1
	if start > 0 {
		line, used = " … ", 3
	}
	for i := start; i < len(m.tokens); i++ {
		chip := m.tokenChip(i, width)
		cells := ansi.StringWidth(chip)
		gap := 0
		if used > 1 {
			gap = 1
		}
		if used > 1 && used+gap+cells > width {
			lines = append(lines, line)
			if len(lines) == rows {
				return lines, i - start
			}
			line, used, gap = " ", 1, 0
		}
		if m.focus == tokenizerFocusResults && i == m.selected {
			chip = s.accent.Render("›") + s.focused.Render(strings.TrimPrefix(chip, "›"))
		} else if m.focus != tokenizerFocusResults {
			chip = s.muted.Render(chip)
		}
		line += strings.Repeat(" ", gap) + chip
		used += gap + cells
	}
	lines = append(lines, line)
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines, len(m.tokens) - start
}

func (m *tokenizerEditor) keepSelectionVisible() {
	if m.usable() {
		m.tokenStart = m.resultStart(m.viewWidth())
	}
}

// Keep the current window while selection remains visible. All fit checks stop
// at the viewport boundary, including jumps across a very large token sequence.
func (m *tokenizerEditor) resultStart(width int) int {
	if len(m.tokens) == 0 || m.resultEnd(0, width) == len(m.tokens) {
		return 0
	}
	start := min(max(0, m.tokenStart), m.selected)
	if m.selected < m.resultEnd(start, width) {
		return start
	}
	// Search only the preceding visible window, not the skipped token prefix.
	start = m.selected
	for previous := start - 1; previous >= 0; previous-- {
		if m.resultEnd(previous, width) <= m.selected {
			break
		}
		start = previous
	}
	return start
}

func (m *tokenizerEditor) resultEnd(start, width int) int {
	row, used := 1, 1
	if start > 0 {
		used = 3 // The leading ellipsis indicates earlier tokens.
	}
	for index := start; index < len(m.tokens); index++ {
		cells := ansi.StringWidth(m.tokenChip(index, width))
		gap := 0
		if used > 1 {
			gap = 1
		}
		if used > 1 && used+gap+cells > width {
			row++
			used, gap = 1, 0
		}
		if row > m.resultRows() {
			return index
		}
		used += gap + cells
	}
	return len(m.tokens)
}

func (m *tokenizerEditor) tokenChip(index, width int) string {
	fragment, _, _ := m.fragment(index)
	label := tokenizerFragmentLabel(fragment, min(16, width-5))
	switch m.tab {
	case 1:
		label = strconv.FormatUint(uint64(m.tokens[index].ID), 10)
	case 2:
		size := min(len(fragment), 5)
		label = fmt.Sprintf("% x", []byte(fragment[:size]))
		if size < len(fragment) {
			label += "…"
		}
	}
	marker := " "
	if m.focus == tokenizerFocusResults && index == m.selected {
		marker = "›"
	}
	return marker + "[" + label + "]"
}

func (m *tokenizerEditor) previousPageSize() int {
	width, rows, used, count := m.viewWidth(), 1, 1, 0
	for index := m.selected - 1; index >= 0; index-- {
		cells := ansi.StringWidth(m.tokenChip(index, width))
		gap := 0
		if used > 1 {
			gap = 1
		}
		if used > 1 && used+gap+cells > width {
			rows++
			used, gap = 1, 0
		}
		if rows > m.resultRows() {
			break
		}
		used += gap + cells
		count++
	}
	return max(1, count)
}

func tokenizerClip(text string, width int) string {
	return ansi.Truncate(text, max(1, width), "…")
}

var tokenizerEditorHelp = []string{
	"Text: type or paste exact UTF-8 text.",
	"Enter inserts a newline. Arrows move the cursor.",
	"Home/End move to the line boundaries.",
	"Backspace/Delete remove a complete grapheme.",
	"Ctrl+U removes all text before the cursor.",
	"Tab and Shift+Tab switch Text, options, and tokens.",
	"At the end of text, Down opens options.",
	"Options: Up/Down move; Enter opens choices.",
	"View: Left/Right switch Text, Token IDs, and Bytes.",
	"Tokens: arrows select; Home/End select first/last.",
	"Page Up/Page Down move by one visible page.",
	"Enter opens every byte of the selected token.",
	"Choices: Up/Down choose; Enter applies; Esc cancels.",
	"Escape returns to Text. Ctrl+C exits.",
	"View changes the display without retokenizing.",
	"Tokenizer selects the vocabulary and splitting rules.",
	"Bytes is an advanced view of exact token bytes.",
	"Input is local and never saved. Maximum: 1 MiB.",
	"Long unbroken text can take time. Editing replaces pending work.",
	"Special-token spellings stay ordinary text.",
	"Partial UTF-8 tokens use exact hexadecimal bytes.",
	"Counts exclude request structure and multimodal input.",
	"Scripts: openai tokenizer count --text \"Hello, world!\"",
	"Files: openai tokenizer inspect --file prompt.txt",
	"JSON: add --format json to count or inspect.",
}

func (m *tokenizerEditor) modalView(s tokenizerEditorStyles) string {
	count := max(1, m.viewHeight()-3)
	_, total := m.modalRows(0, 0)
	start := max(0, min(m.scroll, total-count))
	rows, _ := m.modalRows(start, count)
	title := "Tokenizer · controls"
	if m.modal == 2 {
		title = "Token details · exact bytes"
	}
	lines := []string{s.title.Render(title)}
	lines = append(lines, rows...)
	lines = append(lines, s.muted.Render(fmt.Sprintf("Rows %d–%d of %d", min(start+1, total), min(start+len(rows), total), total)))
	lines = append(lines, s.muted.Render("↑↓ scroll · Esc back · Ctrl+C exit"))
	return m.fit(lines, true)
}

// Retain only visible rows, even when one token contains a large byte sequence.
func (m *tokenizerEditor) modalRows(start, count int) ([]string, int) {
	width := m.viewWidth()
	var rows []string
	total := 0
	emit := func(line string) {
		if total >= start && len(rows) < count {
			rows = append(rows, line)
		}
		total++
	}
	wrap := func(text string) {
		for _, line := range strings.Split(ansi.Wrap(text, width, ""), "\n") {
			emit(line)
		}
	}
	if m.modal == 1 {
		for _, line := range tokenizerEditorHelp {
			line = strings.ReplaceAll(line, "openai tokenizer", readable.Text(m.invocation)+" tokenizer")
			wrap(line)
		}
		return rows, total
	}
	if len(m.tokens) == 0 {
		emit("No current token result.")
		return rows, total
	}
	fragment, begin, end := m.fragment(m.selected)
	wrap(fmt.Sprintf("Token %d of %d · ID %d", m.selected+1, len(m.tokens), m.tokens[m.selected].ID))
	wrap(fmt.Sprintf("Encoding %s · bytes [%d, %d)", m.encoding, begin, end))
	if utf8.ValidString(fragment) {
		emit("Text (escaped):")
		// Quote one rune at a time to bound temporary allocations.
		var row strings.Builder
		used := 0
		for _, value := range fragment {
			escaped := strconv.QuoteRuneToASCII(value)
			escaped = escaped[1 : len(escaped)-1]
			cells := ansi.StringWidth(escaped)
			if used > 0 && used+cells > width {
				emit(row.String())
				row.Reset()
				used = 0
			}
			row.WriteString(escaped)
			used += cells
		}
		emit(row.String())
	} else {
		wrap("Text: partial UTF-8; use the exact bytes below.")
	}
	emit("Hex:")
	perRow := max(1, (width+1)/3)
	for offset := 0; offset < len(fragment); offset += perRow {
		emit(fmt.Sprintf("% x", []byte(fragment[offset:min(len(fragment), offset+perRow)])))
	}
	return rows, total
}
