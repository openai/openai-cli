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
	"github.com/openai/openai-cli/internal/tokenizer"
)

func (m *tokenizerEditor) viewHeight() int { return min(18, max(0, m.height-1)) }
func (m *tokenizerEditor) viewWidth() int  { return max(1, min(100, m.width-4)) }
func (m *tokenizerEditor) roomy() bool     { return m.width >= 50 && m.viewHeight() >= 16 }
func (m *tokenizerEditor) sourceRows() int {
	if m.roomy() {
		return 3
	}
	return 1
}
func (m *tokenizerEditor) resultRows() int {
	if m.roomy() {
		return 2
	}
	return 1
}

type tokenizerEditorStyles struct {
	title, muted, accent, selected, focused, border, input lipgloss.Style
	tokenSelected                                          lipgloss.Style
	tokens                                                 [6]lipgloss.Style
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
	palette := [6]string{"#E9F1FE", "#E7F5ED", "#FBF0D9", "#F2EAFE", "#FBE8EC", "#E4F3F5"}
	if m.dark {
		palette = [6]string{"#263D57", "#26483F", "#51452C", "#423454", "#503438", "#294852"}
	}
	for i, color := range palette {
		s.tokens[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(text)).Background(lipgloss.Color(color))
	}
	s.title = s.title.Bold(true)
	s.muted = s.muted.Foreground(lipgloss.Color(muted))
	s.accent = s.accent.Foreground(lipgloss.Color(focus)).Bold(true)
	s.selected = s.selected.Foreground(lipgloss.Color(text)).Background(lipgloss.Color(fill))
	s.focused = s.selected.Bold(true)
	selectedText := "#FFFFFF"
	if m.dark {
		selectedText = "#101318"
	}
	s.tokenSelected = lipgloss.NewStyle().Foreground(lipgloss.Color(selectedText)).Background(lipgloss.Color(focus))
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
	textLabel := "Text"
	if len(m.lineStarts) > m.sourceRows() {
		textLabel += fmt.Sprintf(" · %d/%d", m.lineIndex(m.cursor)+1, len(m.lineStarts))
	}
	if m.roomy() {
		caption := "╭─ " + textLabel + " "
		lines = append(lines, s.border.Render(caption+strings.Repeat("─", width-ansi.StringWidth(caption)-1)+"╮"))
		for _, line := range m.editorLines(width-4, m.sourceRows()) {
			input := s.input
			if line == "" || line == tokenizerCaret(" ") {
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
		prefix := "  " + textLabel + " "
		if m.focus == tokenizerFocusText {
			prefix = "› " + textLabel + " "
		}
		caption := s.muted
		if m.focus == tokenizerFocusText {
			caption = s.accent
		}
		lines = append(lines, caption.Render(prefix)+m.editorLines(width-ansi.StringWidth(prefix), m.sourceRows())[0])
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
	views := m.optionRow(s, "View", strings.Join(tabs, "  "), viewActive)
	if ansi.StringWidth(views+"  ←→") <= width {
		views += s.muted.Render("  ←→")
	}
	lines = append(lines, views)
	encodingActive := m.focus == tokenizerFocusOptions && m.option == 1
	model, badge := tokenizerModelLabel(m.encoding)
	model += "  " + badge
	if encodingActive {
		model = s.focused.Render(model)
	}
	modelRow := m.optionRow(s, "Model", model, encodingActive)
	if ansi.StringWidth(modelRow+"  ›") <= width {
		modelRow += s.muted.Render("  ›")
	}
	lines = append(lines, modelRow)
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
	if m.focus == tokenizerFocusResults {
		lines = append(lines, s.accent.Render("› "+position))
	} else if len(m.tokens) != 1 {
		lines = append(lines, "  "+s.muted.Render(position))
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
	return marker + caption.Render(fmt.Sprintf("%-7s", label)) + value
}

func tokenizerModelLabel(encoding string) (string, string) {
	switch encoding {
	case "o200k_base":
		return "GPT-5.x & o1/o3", "Default"
	case "cl100k_base":
		return "GPT-4 / GPT-3.5", "Legacy"
	case "r50k_base":
		return "GPT-3", "Legacy"
	case "p50k_base":
		return "Codex", "Legacy"
	default:
		return "Unknown", ""
	}
}

func (m *tokenizerEditor) highlightRow(s tokenizerEditorStyles, value string, active bool) string {
	value = tokenizerClip(value, m.viewWidth()-2)
	if active {
		return s.accent.Render("› ") + s.focused.Render(value+strings.Repeat(" ", max(0, m.viewWidth()-2-ansi.StringWidth(value))))
	}
	return "  " + value
}

func (m *tokenizerEditor) footer() string {
	footer := "Ctrl+C exit · ↑↓ move · Enter select"
	switch m.focus {
	case tokenizerFocusText:
		footer = "Ctrl+C exit · Tab options"
		if m.lineEnd(m.cursor) == len(m.text) {
			footer = "Ctrl+C exit · ↓ options · Tab switch"
		}
		if len(m.lineStarts) > m.sourceRows() && ansi.StringWidth(footer+" · PgUp/PgDn") <= m.viewWidth() {
			footer += " · PgUp/PgDn"
		}
	case tokenizerFocusResults:
		footer = "Ctrl+C exit · ←→ token · Enter details"
		if ansi.StringWidth(footer) > m.viewWidth() {
			footer = "Ctrl+C exit · ←→ · Enter details"
		}
		if ansi.StringWidth(footer+" · ↑ settings") <= m.viewWidth() {
			footer += " · ↑ settings"
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
	return footer
}

func (m *tokenizerEditor) choiceView(s tokenizerEditorStyles) string {
	title := "Choose view"
	labels := []string{"Text", "Token IDs", "Bytes"}
	descriptions := []string{"Readable pieces", "Numeric token IDs", "Exact hex bytes"}
	labelWidth := 14
	current := m.tab
	var encodings []string
	if m.modal == tokenizerModalEncoding {
		title, labelWidth = "Choose model", 22
		encodings = tokenizer.SupportedEncodings()
		labels, descriptions = make([]string, len(encodings)), make([]string, len(encodings))
		current = 0
		for i, encoding := range encodings {
			labels[i], descriptions[i] = tokenizerModelLabel(encoding)
			if m.encoding == encoding {
				current = i
			}
		}
	}
	lines := []string{s.title.Render(title)}
	lines = append(lines, "")
	for i, label := range labels {
		if i == current {
			label += " ✓"
		}
		description := descriptions[i]
		if i != m.choice {
			description = s.muted.Render(description)
		}
		line := fmt.Sprintf("%-*s%s", labelWidth, label, description)
		if ansi.StringWidth(line) > m.viewWidth()-2 {
			line = label
		}
		lines = append(lines, m.highlightRow(s, line, i == m.choice))
		if m.modal == tokenizerModalEncoding {
			switch encodings[i] {
			case "o200k_base":
				lines = append(lines, "  "+s.muted.Render("GPT-4o / 4.1 / 4.5 · o4-mini"))
			case "cl100k_base":
				lines = append(lines, "  "+s.muted.Render("Original GPT-4 / Turbo"))
			}
		}
	}
	footer := "Ctrl+C exit · ↑↓ move · Enter select · Esc cancel"
	lines = append(lines, "")
	if ansi.StringWidth(footer) <= m.viewWidth() {
		lines = append(lines, s.muted.Render(footer))
	} else {
		lines = append(lines, s.muted.Render("Ctrl+C exit · ↑↓ move"), s.muted.Render("Enter select · Esc cancel"))
	}
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
	lineIndex := m.lineIndex(m.cursor)
	if count > 1 && lineIndex > 0 {
		lineIndex--
	}
	lines := make([]string, 0, count)
	for len(lines) < count {
		start := m.lineStarts[lineIndex]
		end := len(m.text)
		if lineIndex+1 < len(m.lineStarts) {
			end = m.lineStarts[lineIndex+1] - 1
		}
		cursor := -1
		if m.cursor >= start && m.cursor <= end && m.focus == 0 {
			cursor = m.cursor
		}
		line := m.editorLine(start, end, cursor, width)
		if m.text == "" && len(lines) == 0 {
			line = "Type or paste text…"
			if m.focus == 0 {
				line = tokenizerCaret(" ") + line
			}
		}
		lines = append(lines, tokenizerClip(line, width))
		if end == len(m.text) {
			break
		}
		lineIndex++
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
		caretWidth := 1
		if cursor < end && index+1 < len(m.boundaries) {
			caretWidth = tokenizerSourceWidth(tokenizerSourceDisplay(m.text[cursor:min(m.boundaries[index+1], end)]))
		}
		used := 0
		for index > first {
			part := m.text[m.boundaries[index-1]:min(m.boundaries[index], end)]
			cells := tokenizerSourceWidth(tokenizerSourceDisplay(part))
			if used+cells > max(0, width-caretWidth-2) {
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
		if offset == end || index == len(m.boundaries)-1 {
			if offset == cursor {
				out.WriteString(tokenizerCaret(" "))
			}
			break
		}
		part := m.text[offset:min(m.boundaries[index+1], end)]
		display := tokenizerSourceDisplay(part)
		cells := tokenizerSourceWidth(display)
		if used+cells > width-1 {
			if offset == cursor {
				out.WriteString(tokenizerCaret("…"))
			} else {
				out.WriteRune('…')
			}
			break
		}
		if offset == cursor {
			out.WriteString(tokenizerCaret(display))
		} else {
			out.WriteString(display)
		}
		used += cells
		index++
	}
	return out.String()
}

// Inverse video marks the existing cell without inserting a source character.
// Keep this monochrome rendition under NO_COLOR so the caret remains visible.
func tokenizerCaret(display string) string { return "\x1b[7m" + display + "\x1b[27m" }

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
		available := width - 2
		if len(fragment)*2 > available {
			available-- // Keep complete byte pairs before a truncation marker.
		}
		bytes := []byte(fragment[:min(len(fragment), max(1, available/2))])
		label := "0x" + hex.EncodeToString(bytes)
		if len(bytes) < len(fragment) {
			label += "…"
		}
		return tokenizerSegmentClip(label, width)
	}
	prefix := fragment[:min(len(fragment), max(1, width)*4)]
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	label := tokenizerSourceControls.Replace(readable.Text(prefix))
	if tokenizerSourceWidth(label) == 0 {
		quoted := strconv.QuoteToASCII(prefix)
		label = quoted[1 : len(quoted)-1]
	}
	if len(prefix) != len(fragment) {
		label += "…"
	}
	return tokenizerSegmentClip(label, width)
}

func tokenizerSegmentClip(label string, width int) string {
	if tokenizerSourceWidth(label) <= width {
		return label
	}
	var out strings.Builder
	used := 0
	for len(label) > 0 {
		sequence, cells, read := imagePickerSequence(label)
		if used+cells > width-1 {
			break
		}
		out.WriteString(sequence)
		used += cells
		label = label[read:]
	}
	return out.String() + "…"
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
	lines := make([]string, rows)
	lines[0] = "  "
	if start > 0 {
		lines[0] = "… "
	}
	end := m.walkResultSegments(start, width, func(index, row, gap int, label string) {
		if lines[row] == "" {
			lines[row] = "  "
		}
		active := m.focus == tokenizerFocusText || m.focus == tokenizerFocusResults
		if active && index == m.selected && !m.updating && !m.failed {
			if m.color {
				// Keep the whole grapheme span together, including whitespace.
				label = s.tokenSelected.Render("\x1b[1;4m" + label + "\x1b[22;24m")
			} else {
				label = "\x1b[4;7m" + label + "\x1b[24;27m"
			}
		} else {
			label = s.tokens[index%len(s.tokens)].Render(label)
		}
		lines[row] += strings.Repeat(" ", gap) + label
	})
	return lines, end - start
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
	return m.walkResultSegments(start, width, nil)
}

// Painting and paging share a fixed gutter and the same conservative widths.
func (m *tokenizerEditor) walkResultSegments(start, width int, visit func(index, row, gap int, label string)) int {
	row, used := 0, 2
	for index := start; index < len(m.tokens); index++ {
		label := m.tokenSegment(index, width)
		cells := tokenizerSourceWidth(label)
		gap := 0
		if used > 2 {
			gap = m.tab // Text joins directly; IDs use one space, Bytes uses two.
		}
		if used > 2 && used+gap+cells > width {
			row++
			used, gap = 2, 0
		}
		if row >= m.resultRows() {
			return index
		}
		if visit != nil {
			visit(index, row, gap, label)
		}
		used += gap + cells
	}
	return len(m.tokens)
}

func (m *tokenizerEditor) tokenSegment(index, width int) string {
	if m.tab == 1 {
		return strconv.FormatUint(uint64(m.tokens[index].ID), 10)
	}
	fragment, _, _ := m.fragment(index)
	if m.tab == 2 {
		size := min(len(fragment), 5)
		label := fmt.Sprintf("% x", []byte(fragment[:size]))
		if size < len(fragment) {
			label += "…"
		}
		return label
	}
	return tokenizerFragmentLabel(fragment, max(1, width-2))
}

func (m *tokenizerEditor) previousPageSize() int {
	start := m.selected
	// Use the rendered window's budget, including its continuation marker.
	// The scan stops within one preceding viewport, even for large sequences.
	for previous := m.selected - 1; previous >= 0; previous-- {
		if m.resultEnd(previous, m.viewWidth()) < m.selected {
			break
		}
		start = previous
	}
	return max(1, m.selected-start)
}

func tokenizerClip(text string, width int) string {
	return ansi.Truncate(text, max(1, width), "…")
}

var tokenizerEditorHelp = []string{
	"Text: type or paste exact UTF-8 text.",
	"Enter inserts a newline. Arrows move the cursor.",
	"The highlighted token follows the text cursor.",
	"Home/End move to the line boundaries.",
	"Ctrl+Home/End move to the document boundaries.",
	"Ctrl+Left/Right skip words separated by whitespace.",
	"Page Up/Page Down move by one visible text page.",
	"Overflowing text shows the current line and total lines.",
	"Backspace/Delete remove a complete grapheme.",
	"Ctrl+U removes all text before the cursor.",
	"Tab and Shift+Tab switch Text, options, and tokens.",
	"On the last line, Down opens options.",
	"Options: Up/Down move; Enter opens choices.",
	"View: Left/Right switch Text, Token IDs, and Bytes.",
	"Tokens: Left/Right select; Home/End select first/last.",
	"Up returns from Tokens to the Model row.",
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
	_, total := m.modalRows(0, 0)
	count := m.modalPageSize(total)
	start := max(0, min(m.scroll, total-count))
	rows, _ := m.modalRows(start, count)
	if m.modal == tokenizerModalHelp {
		lines := []string{s.title.Render("Tokenizer · controls")}
		lines = append(lines, rows...)
		lines = append(lines, s.muted.Render(fmt.Sprintf("Rows %d–%d of %d", min(start+1, total), min(start+len(rows), total), total)))
		lines = append(lines, s.muted.Render("↑↓ scroll · Esc back · Ctrl+C exit"))
		return m.fit(lines, true)
	}
	title := "Token details"
	if len(m.tokens) > 0 {
		title = fmt.Sprintf("Token %d of %d", m.selected+1, len(m.tokens))
	}
	model, badge := tokenizerModelLabel(m.encoding)
	if badge != "" {
		model += " · " + badge
	}
	lines := []string{s.title.Render(title), s.muted.Render(model), ""}
	for _, row := range rows {
		if len(row) >= tokenizerDetailLabelWidth {
			switch strings.TrimSpace(row[:tokenizerDetailLabelWidth]) {
			case "Text", "Token ID", "Bytes", "Hex", "":
				row = s.muted.Render(row[:tokenizerDetailLabelWidth]) + row[tokenizerDetailLabelWidth:]
			}
		}
		lines = append(lines, row)
	}
	lines = append(lines, "")
	footer := "Ctrl+C exit · Esc back"
	if total > count {
		lines = append(lines, s.muted.Render(fmt.Sprintf("Rows %d–%d of %d", start+1, start+len(rows), total)))
		footer += " · ↑↓ scroll"
	}
	lines = append(lines, s.muted.Render(footer))
	return m.fit(lines, true)
}

func (m *tokenizerEditor) modalPageSize(total int) int {
	if m.modal == tokenizerModalHelp {
		return max(1, m.viewHeight()-3)
	}
	count := max(1, m.viewHeight()-5)
	if total > count {
		count = max(1, count-1)
	}
	return count
}

const tokenizerDetailLabelWidth = 10

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
	if m.modal == tokenizerModalHelp {
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
	padding := strings.Repeat(" ", tokenizerDetailLabelWidth)
	valueWidth := max(1, width-tokenizerDetailLabelWidth)
	field := func(label, value string) {
		prefix := fmt.Sprintf("%-*s", tokenizerDetailLabelWidth, label)
		for _, line := range strings.Split(ansi.Wrap(value, valueWidth, ""), "\n") {
			emit(prefix + line)
			prefix = padding
		}
	}
	if utf8.ValidString(fragment) {
		// Quote string fragments with one reusable buffer. Single-rune quoting
		// uses different rules for quotes and apostrophes.
		var scratch [16]byte
		var row strings.Builder
		used := 0
		prefix := fmt.Sprintf("%-*s", tokenizerDetailLabelWidth, "Text")
		visible := func() bool { return total >= start && len(rows) < count }
		flush := func() {
			if visible() {
				emit(prefix + row.String())
			} else {
				total++
			}
			row.Reset()
			used, prefix = 0, padding
		}
		write := func(escaped string) {
			if used > 0 && used+len(escaped) > valueWidth {
				flush()
			}
			if visible() {
				row.WriteString(escaped)
			}
			used += len(escaped)
		}
		write(`"`)
		for offset, value := range fragment {
			quoted := strconv.AppendQuoteToASCII(scratch[:0], fragment[offset:offset+utf8.RuneLen(value)])
			write(string(quoted[1 : len(quoted)-1]))
		}
		write(`"`)
		flush()
	} else {
		field("Text", "partial UTF-8; see Hex.")
	}
	field("Token ID", strconv.FormatUint(uint64(m.tokens[m.selected].ID), 10))
	emit("")
	field("Bytes", fmt.Sprintf("%d · offset %d", end-begin, begin))
	perRow := max(1, (valueWidth+1)/3)
	hexRows := (len(fragment) + perRow - 1) / perRow
	// Unseen hexadecimal rows need only arithmetic, not formatted strings.
	for index := max(0, start-total); index < min(hexRows, start+count-total); index++ {
		prefix := padding
		if index == 0 {
			prefix = fmt.Sprintf("%-*s", tokenizerDetailLabelWidth, "Hex")
		}
		offset := index * perRow
		rows = append(rows, prefix+fmt.Sprintf("% x", []byte(fragment[offset:min(len(fragment), offset+perRow)])))
	}
	total += hexRows
	return rows, total
}
