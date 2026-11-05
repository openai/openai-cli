package tokenize

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/tokenizer"
)

func (m *tokenizerEditor) roomy() bool {
	return m.studioLayout() || m.width >= 50 && m.viewHeight() >= 16
}
func (m *tokenizerEditor) sourceRows() int {
	if m.studioLayout() {
		return min(8, m.bodyHeight()/3)
	}
	if m.roomy() {
		return 3
	}
	return 1
}
func (m *tokenizerEditor) resultRows() int {
	if m.studioLayout() {
		// Reserve a count row, a byte-count row and up to four rows of selected-token details.
		return min(4, max(1, m.bodyHeight()-6))
	}
	if m.roomy() {
		return 2
	}
	return 1
}

type tokenizerEditorStyles struct {
	muted, accent, selected, focused, border, warning, failure lipgloss.Style
	fill, edge, secondary                                      string
	tokenSelected, sourceSelected                              lipgloss.Style
	tokens, byteCounts                                         [6]lipgloss.Style
}

func (m *tokenizerEditor) styles() tokenizerEditorStyles {
	s := tokenizerEditorStyles{}
	if !m.color {
		s.tokenSelected = lipgloss.NewStyle().Reverse(true)
		s.sourceSelected = s.tokenSelected
		return s
	}
	warning, failure, tokenInk := "#885B14", "#B23A3A", "#FFFFFF"
	focus, fill, text, muted, border := "#3159BC", "#E9EFFE", "#20283B", "#657087", "#BAC4D8"
	if m.dark {
		warning, failure, tokenInk = "#E2BC79", "#E59A9A", "#172338"
		focus, fill, text, muted, border = "#8AA8FF", "#343D58", "#F2F4FA", "#A4ACC2", "#51566B"
	}
	// Alternate cool and warm fills. Each token shares its ink with its byte count.
	palette := [6][2]string{
		{"#C6ECE9", "#17615C"}, {"#FADCC8", "#884B2B"}, {"#E2D9F7", "#61478B"},
		{"#F4E4AE", "#71551B"}, {"#D9E9BF", "#2E592C"}, {"#E7BFD3", "#793245"},
	}
	if m.dark {
		palette = [6][2]string{
			{"#285154", "#A1E5DF"}, {"#75472C", "#FFD5AE"}, {"#563D79", "#DCC7FA"},
			{"#674F29", "#EEDB9A"}, {"#295B2D", "#D6E7AD"}, {"#763D59", "#F7C6D8"},
		}
	}
	for i, pair := range palette {
		s.byteCounts[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(pair[1]))
		s.tokens[i] = s.byteCounts[i].Background(lipgloss.Color(pair[0]))
	}
	s.fill, s.edge, s.secondary = fill, border, muted
	s.warning = s.warning.Foreground(lipgloss.Color(warning))
	s.failure = s.failure.Foreground(lipgloss.Color(failure))
	s.muted = s.muted.Foreground(lipgloss.Color(muted))
	s.accent = s.accent.Foreground(lipgloss.Color(focus))
	s.selected = s.selected.Foreground(lipgloss.Color(text)).Background(lipgloss.Color(fill))
	s.sourceSelected = s.selected
	s.focused = s.selected.Bold(true)
	s.tokenSelected = lipgloss.NewStyle().Foreground(lipgloss.Color(tokenInk)).Background(lipgloss.Color(focus))
	s.border = s.border.Foreground(lipgloss.Color(border))
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
		lines = lines[:min(len(lines), m.height)]
		for i, line := range lines {
			lines[i] = tokenizerClip(line, m.width)
		}
		view.Content = strings.Join(lines, "\n")
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
	if m.studioLayout() {
		width = m.sourceWidth()
	}
	lines, cursor := m.textRows(s, width)
	if cursor != nil {
		cursor.X += 2 // Left panel inset.
		cursor.Y += 2 // Header and divider.
		if m.framed() {
			cursor.Y++
		}
		cursor.Shape, cursor.Blink = tea.CursorBar, true
		view.Cursor = cursor
	}
	if m.studioLayout() {
		view.Content = m.studioView(s, lines)
		return view
	}
	lines = append(lines, m.settingsRows(s, width)...)
	position := "Tokens"
	spaceLabel := ""
	if len(m.tokens) > 0 {
		position = fmt.Sprintf("Token %d of %d", m.selected+1, len(m.tokens))
		fragment, _, _ := m.fragment(m.selected)
		spaceLabel = tokenizerSpaceLabel(fragment)
		if !utf8.ValidString(fragment) {
			position += " (partial UTF-8)"
		}
	}
	results := trimEmptyRows(m.resultLines(s, width))
	if m.focus == tokenizerFocusResults || len(m.tokens) != 1 || spaceLabel != "" {
		heading := s.heading(position, m.focus == tokenizerFocusResults)
		if spaceLabel != "" {
			if ansi.StringWidth(heading)+2+len(spaceLabel) > width {
				lines = append(lines, heading)
				heading = ""
			}
			heading += "  " + s.tokenSelected.Render(spaceLabel)
		}
		if len(results) == 1 && tokenizerSourceWidth(heading+results[0]) <= width {
			results[0] = heading + results[0]
		} else {
			lines = append(lines, heading)
		}
	}
	lines = append(lines, results...)
	lines = append(lines, m.noteRows(s, width)...)
	view.Content = m.panel(s, "Tokenizer", m.countStatus(s), lines, m.footer(s))
	return view
}

func (s tokenizerEditorStyles) heading(label string, active bool) string {
	if active {
		return s.focused.Render("› " + label)
	}
	return "  " + s.muted.Render(label)
}

func (m *tokenizerEditor) sourceLabel() (string, int) {
	total := len(m.lineStarts)
	if total <= m.sourceRows() {
		return "Text", 9
	}
	label := fmt.Sprintf("Text %d/%d", m.lineIndex(m.boundaries[m.sourceRange().first])+1, total)
	if !m.studioLayout() {
		return label, len(label) + 3
	}
	// Reserve both line numbers at their widest so paging keeps the same wrap.
	return label, 3 + len("Text /") + 2*len(strconv.Itoa(total))
}

func (m *tokenizerEditor) textRows(s tokenizerEditorStyles, width int) ([]string, *tea.Cursor) {
	label, indent := m.sourceLabel()
	prefix := s.heading(label, m.focus == tokenizerFocusText) + strings.Repeat(" ", indent-2-len(label))
	sourceLines := m.editorLines
	if m.studioLayout() {
		sourceLines = m.studioSourceLines
	}
	lines, cursor := sourceLines(s, width-indent, m.sourceRows())
	lines = trimEmptyRows(lines)
	if cursor != nil {
		cursor.X += indent
	}
	for i, line := range lines {
		if i == 0 {
			lines[i] = prefix + line
		} else {
			lines[i] = strings.Repeat(" ", indent) + line
		}
	}
	return lines, cursor
}

func (m *tokenizerEditor) settingsRows(s tokenizerEditorStyles, width int) []string {
	tabs := []string{"Text", "Token IDs", "Bytes"}
	model, badge := tokenizerModelLabel(m.encoding)
	return []string{
		m.optionRow(s, width, 0, "View", tabs, m.tab, "←→"),
		m.optionRow(s, width, 1, "Model", []string{model, badge}, 0, "›"),
	}
}

func (m *tokenizerEditor) noteRows(s tokenizerEditorStyles, width int) []string {
	var lines []string
	if m.note != "" {
		style := s.warning
		if m.failed {
			style = s.failure
		}
		for _, line := range strings.Split(ansi.Wrap(m.note, width-2, ""), "\n") {
			lines = append(lines, "  "+style.Render(line))
		}
	}
	return lines
}

func (m *tokenizerEditor) countStatus(s tokenizerEditorStyles) string {
	var counts []string
	style := s.muted.Bold(m.color)
	if m.failed {
		counts = append(counts, "Count unavailable")
		style = s.failure
	} else if !m.updating {
		label := "tokens"
		if len(m.tokens) == 1 {
			label = "token"
		}
		counts = append(counts, fmt.Sprintf("%d %s", len(m.tokens), label))
	}
	if m.tab == 2 {
		counts = append(counts, tokenizerByteSize(len(m.text)))
	}
	if len(counts) == 0 {
		return ""
	}
	return style.Render(strings.Join(counts, "  "))
}

func tokenizerByteSize(n int) string {
	if n == 1 {
		return "1 byte"
	}
	return strconv.Itoa(n) + " bytes"
}

func (m *tokenizerEditor) optionRow(s tokenizerEditorStyles, width, index int, label string, values []string, current int, hint string) string {
	focused := m.focus == tokenizerFocusOptions && m.option == index
	labelStyle, secondary, active := s.muted, s.muted, lipgloss.NewStyle()
	if index == 0 {
		secondary = lipgloss.NewStyle()
		active = s.accent.Bold(true)
	}
	marker := "  "
	if focused {
		labelStyle = s.selected
		secondary = s.muted.Inherit(s.selected)
		active = s.accent.Inherit(s.selected).Bold(index == 0 || m.color)
		markerStyle := s.focused
		if index == 1 {
			markerStyle = active
		}
		marker = markerStyle.Render("› ")
	}
	labelWidth, gap := 7, "  "
	if index == 0 {
		// Each View reserves its brackets, including the first cell before Text.
		labelWidth, gap = 6, ""
	}
	for i, value := range values {
		style := secondary
		if i == current {
			style = active
			if index == 0 {
				value = "[" + value + "]"
			}
		} else if index == 0 {
			value = " " + value + " "
		}
		values[i] = style.Render(value)
	}
	row := marker + labelStyle.Render(fmt.Sprintf("%-*s", labelWidth, label)) + strings.Join(values, secondary.Render(gap))
	if ansi.StringWidth(row+"  "+hint) <= width {
		hintStyle := secondary
		if index == 0 {
			hintStyle = s.accent.Inherit(secondary)
		}
		row += hintStyle.Render("  " + hint)
	}
	if focused {
		row += s.selected.Render(strings.Repeat(" ", max(0, width-ansi.StringWidth(row))))
	}
	return row
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

func (m *tokenizerEditor) highlightRow(s tokenizerEditorStyles, label, detail string, labelWidth int, active bool) string {
	gap := strings.Repeat(" ", max(1, labelWidth-ansi.StringWidth(label)))
	if !active {
		return "  " + label + gap + s.muted.Render(detail)
	}
	padding := strings.Repeat(" ", max(0, m.viewWidth()-ansi.StringWidth(label+gap+detail)-2))
	focused := s.focused
	if m.modal == tokenizerModalView {
		focused = s.accent.Inherit(s.selected).Bold(true)
	}
	return s.selected.Render("› ") + focused.Render(label) + s.selected.Render(gap+detail+padding)
}

func (m *tokenizerEditor) choiceView(s tokenizerEditorStyles) string {
	title := "Choose view"
	labels := []string{"Text", "Token IDs", "Bytes"}
	descriptions := []string{"Text in each token", "Number per token", "UTF-8 bytes in hex"}
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
	status := fmt.Sprintf("%d views", len(labels))
	if m.modal == tokenizerModalEncoding {
		status = fmt.Sprintf("%d models", len(labels))
	}
	var lines []string
	for i, label := range labels {
		if i == current {
			label += " ✓"
		}
		description, related := descriptions[i], ""
		if m.modal == tokenizerModalEncoding {
			switch encodings[i] {
			case "o200k_base":
				related = "GPT-4o / 4.1 / 4.5, o4-mini"
			case "cl100k_base":
				related = "Original GPT-4 / Turbo"
			}
		}
		if related != "" && 2+labelWidth+ansi.StringWidth(description+"  "+related) <= m.viewWidth() {
			description, related = description+"  "+related, ""
		}
		lines = append(lines, m.highlightRow(s, label, description, labelWidth, i == m.choice))
		if related != "" {
			lines = append(lines, "  "+s.muted.Render(related))
		}
	}
	footer := s.shortcutFooter(m.viewWidth(), []string{"↑↓ choose", "Enter apply"}, []string{"Esc cancel"})
	return m.panel(s, title, s.muted.Render(status+"  ✓ applied"), lines, footer)
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

func (m *tokenizerEditor) editorLines(s tokenizerEditorStyles, width, count int) ([]string, *tea.Cursor) {
	span := m.sourceRange()
	anchor := m.boundaries[span.first]
	lineIndex := m.lineIndex(anchor)
	lastLine := m.lineIndex(m.boundaries[max(span.first, span.end-1)])
	if count > lastLine-lineIndex+1 && lineIndex > 0 {
		lineIndex--
	}
	lines := make([]string, 0, count)
	var caret *tea.Cursor
	for len(lines) < count {
		start := m.lineStarts[lineIndex]
		end := len(m.text)
		if lineIndex+1 < len(m.lineStarts) {
			end = m.lineStarts[lineIndex+1] - 1
		}
		line, column := m.editorLine(start, end, width, span, s.sourceSelected)
		if column >= 0 {
			caret = tea.NewCursor(column, len(lines))
		}
		if m.text == "" && len(lines) == 0 {
			line = s.muted.Render("Type or paste text…")
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
	return lines, caret
}

func (m *tokenizerEditor) editorLine(start, end, width int, span tokenizerSourceRange, style lipgloss.Style) (string, int) {
	// Bound work by visible graphemes, using the index built when text changes.
	// A cursor near the end of a large line must not scan its complete prefix.
	first := sort.SearchInts(m.boundaries, start)
	index := first
	anchor := m.boundaries[span.first]
	if anchor >= start && anchor <= end && (m.focus == tokenizerFocusText || span.first < span.end) {
		index = span.first
		// Reserve enough space for the selected token on this line before showing left context.
		focusWidth := 0
		limit := min(max(index+1, span.end), len(m.boundaries)-1)
		for i := index; i < limit && m.boundaries[i] <= end && focusWidth < width; i++ {
			display, _ := tokenizerSourceCell(m.text[m.boundaries[i]:m.boundaries[i+1]], span.contains(i))
			focusWidth += tokenizerSourceWidth(display)
		}
		used := 0
		for index > first {
			part := m.text[m.boundaries[index-1]:min(m.boundaries[index], end)]
			cells := tokenizerSourceWidth(tokenizerSourceDisplay(part))
			if used+cells > max(0, width-max(1, focusWidth)-2) {
				break
			}
			used += cells
			index--
		}
	}
	var out tokenizerSourceRow
	if index > first {
		out.WriteRune('…')
	}
	used := tokenizerSourceWidth(out.String())
	column := -1
	for index < len(m.boundaries) && m.boundaries[index] <= end {
		offset := m.boundaries[index]
		if offset == anchor && m.focus == tokenizerFocusText {
			column = used
		}
		selected := span.contains(index)
		if (offset == end && !selected) || index == len(m.boundaries)-1 {
			if offset == anchor && m.focus == tokenizerFocusText {
				out.WriteByte(' ')
			}
			break
		}
		display, _ := tokenizerSourceCell(m.text[offset:m.boundaries[index+1]], selected)
		cells := tokenizerSourceWidth(display)
		if used+cells > width-1 {
			out.appendCell("…", selected)
			break
		}
		out.appendCell(display, selected)
		used += cells
		index++
	}
	return out.render(style), column
}

// Source windows and padding must share the inline painter's conservative
// grapheme widths, including terminals that render a ZWJ cluster as scalars.
func tokenizerSourceWidth(text string) int {
	width := 0
	for len(text) > 0 {
		_, cells, read := cliterminal.Sequence(text)
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

// Space separators are blank in the strip. Tabs and newlines already have visible escapes.
func tokenizerSpaceLabel(fragment string) string {
	count := 0
	for _, r := range fragment {
		if r != ' ' && !unicode.Is(unicode.Zs, r) {
			return ""
		}
		count++
	}
	if count == 0 {
		return ""
	}
	unit := "space"
	if count != len(fragment) {
		unit = "Unicode space"
	}
	if count != 1 {
		unit += "s"
	}
	return strconv.Itoa(count) + " " + unit
}

func tokenizerSegmentClip(label string, width int) string {
	if tokenizerSourceWidth(label) <= width {
		return label
	}
	var out strings.Builder
	used := 0
	for len(label) > 0 {
		sequence, cells, read := cliterminal.Sequence(label)
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
	if len(m.tokens) == 0 {
		return nil, 0
	}
	start := m.resultStart(width)
	lines, end := m.tokenStrip(s, width, start)
	return lines, end - start
}

// Return the painted strip and its exclusive ending token index.
func (m *tokenizerEditor) tokenStrip(s tokenizerEditorStyles, width, start int) ([]string, int) {
	lines := make([]string, m.resultRows())
	lines[0] = "  "
	if start > 0 {
		lines[0] = "… "
	}
	separator := " "
	if m.tab == 2 && !m.color {
		separator = "│" // Uncolored byte pairs still need a visible token boundary.
	}
	end := m.walkResultSegments(start, width, m.resultRows(), func(cell tokenizerTokenCell, label string) {
		index, row := cell.index, cell.row
		if lines[row] == "" {
			lines[row] = "  "
		}
		style := s.tokens[index%len(s.tokens)]
		if index == m.selected && !m.updating && !m.failed {
			style = s.tokenSelected
			if m.tab == 0 {
				piece, _, _ := m.fragment(index)
				if tokenizerSpaceLabel(piece) != "" {
					// Mark the first blank cell without changing the strip's width or page breaks.
					_, n := utf8.DecodeRuneInString(label)
					label = "␣" + strings.Repeat(" ", tokenizerSourceWidth(label[:n])-1) + label[n:]
				}
			}
		}
		if cell.column > 2 && m.tab != 0 {
			lines[row] += separator
		}
		lines[row] += style.Render(label)
	})
	return lines, end
}

func (m *tokenizerEditor) keepSelectionVisible() {
	if m.usable() {
		m.tokenStart = m.resultStart(m.resultWidth())
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
	return m.resultStartIncluding(m.selected, width, m.resultRows())
}

// Search only the preceding visible window, even when jumping to the last token.
func (m *tokenizerEditor) resultStartIncluding(last, width, rows int) int {
	start := last
	for previous := last - 1; previous >= 0; previous-- {
		if m.walkResultSegments(previous, width, rows, nil) <= last {
			break
		}
		start = previous
	}
	return start
}

func (m *tokenizerEditor) resultEnd(start, width int) int {
	return m.walkResultSegments(start, width, m.resultRows(), nil)
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

func (m *tokenizerEditor) pageResults(forward bool) {
	width := m.resultWidth()
	start := m.resultStart(width)
	if forward {
		end := m.resultEnd(start, width)
		if end == len(m.tokens) {
			m.selected = max(0, end-1)
			return
		}
		m.priorPageStart, start = start, end
	} else {
		// Several starts can lead to the same boundary. Prefer the page we just left.
		previous := m.priorPageStart
		m.priorPageStart = 0
		if previous < start && m.resultEnd(previous, width) == start {
			start = previous
		} else {
			start = m.resultStartIncluding(max(0, start-1), width, m.resultRows())
		}
	}
	m.selected, m.tokenStart = start, start
}

func tokenizerClip(text string, width int) string {
	return ansi.Truncate(text, max(1, width), "…")
}

var tokenizerEditorHelp = []struct {
	title     string
	shortcuts bool
	items     [][2]string
}{
	{"Controls", true, [][2]string{
		{"Tab", "Inspect tokens or edit at the selected token. Shift+Tab opens settings."},
		{"← →", "In Text, move the cursor. In View, switch views. In Tokens, ← → select tokens and ↑ ↓ move a row."},
		{"PgUp/PgDn", "Page through text, tokens, or dialog rows."},
		{"Ctrl+← →", "Move between words in Text."},
		{"v / m / d", "Outside Text, open View, Model, or Details."},
		{"Enter", "Add a line in Text or open the selection."},
	}},
	{"Views and details", false, [][2]string{
		{"Text", "The text in each token."},
		{"Token IDs", "The number this encoding assigns to each token."},
		{"Bytes", "Each token's UTF-8 bytes in hex."},
		{"Starts", "Bytes before this token (0 for the first)."},
		{"Size", "Number of UTF-8 bytes in the token."},
	}},
}

func (m *tokenizerEditor) modalView(s tokenizerEditorStyles) string {
	_, total := m.modalRows(0, 0)
	count := m.modalPageSize(total)
	start := max(0, min(m.scroll, total-count))
	rows, _ := m.modalRows(start, count)
	pageRange := ""
	var navigation []string
	if total > count {
		pageRange = s.muted.Render(fmt.Sprintf("Rows %d–%d of %d", start+1, start+len(rows), total))
		navigation = []string{"↑↓ scroll"}
		// Scrolling dialogs reserve one footer row for controls.
		if shortcutWidth([]string{"↑↓ scroll", tokenizerPageHint, "Esc back", tokenizerExitHint}) <= m.viewWidth() {
			navigation = append(navigation, tokenizerPageHint)
		}
	}
	controls := s.shortcutFooter(m.viewWidth(), navigation, []string{"Esc back"})
	if m.modal == tokenizerModalHelp {
		return m.panel(s, "Tokenizer help", pageRange, rows, controls)
	}
	title := "Token details"
	if len(m.tokens) > 0 {
		title = fmt.Sprintf("Token %d of %d", m.selected+1, len(m.tokens))
	}
	model, badge := tokenizerModelLabel(m.encoding)
	if badge != "" {
		model += "  " + badge
	}
	for i, row := range rows {
		if len(row) >= tokenizerDetailLabelWidth {
			switch strings.TrimSpace(row[:tokenizerDetailLabelWidth]) {
			case "Text", "Token ID", "Starts", "Size", "Hex", "":
				rows[i] = s.muted.Render(row[:tokenizerDetailLabelWidth]) + row[tokenizerDetailLabelWidth:]
			}
		}
	}
	var footer []string
	if total > count {
		footer = append(footer, pageRange)
	}
	footer = append(footer, controls...)
	return m.panel(s, title, s.muted.Render(model), rows, footer)
}

func (m *tokenizerEditor) modalPageSize(total int) int {
	count := m.bodyHeight()
	if m.modal == tokenizerModalDetails && total > count {
		count-- // Show the visible range above the shortcut footer.
	}
	return max(1, count)
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
	if m.modal == tokenizerModalHelp {
		s := m.styles()
		for _, section := range tokenizerEditorHelp {
			emit(s.accent.Bold(m.color).Render(section.title))
			label := s.muted
			if section.shortcuts {
				label = s.accent
			}
			keyWidth := 0
			for _, item := range section.items {
				keyWidth = max(keyWidth, ansi.StringWidth(item[0])+2)
			}
			indent := strings.Repeat(" ", keyWidth)
			for _, item := range section.items {
				prefix := label.Render(item[0]) + strings.Repeat(" ", keyWidth-ansi.StringWidth(item[0]))
				for _, line := range strings.Split(ansi.Wrap(item[1], width-keyWidth, ""), "\n") {
					emit(prefix + line)
					prefix = indent
				}
			}
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
		if spaces := tokenizerSpaceLabel(fragment); spaces != "" {
			write("  " + spaces)
		}
		flush()
	} else {
		field("Text", "partial UTF-8; see Hex.")
	}
	field("Token ID", strconv.FormatUint(uint64(m.tokens[m.selected].ID), 10))
	field("Starts", "After "+tokenizerByteSize(begin))
	field("Size", tokenizerByteSize(end-begin))
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
