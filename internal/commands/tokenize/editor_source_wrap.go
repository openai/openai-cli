package tokenize

import "strings"

// Wrap visible source graphemes without scanning a long draft from the beginning.
func (m *tokenizerEditor) studioSourceLines(s tokenizerEditorStyles, width, count int) []string {
	if m.text == "" {
		return m.editorLines(s, width, count)
	}
	cursor := m.cursorIndex()
	// Start at a fixed grapheme boundary at least one viewport before the caret.
	// A visible grapheme occupies at least one cell or ends a row.
	first := max(0, cursor/width-count) * width
	var rows []string
	var line strings.Builder
	used, caretRow := 0, -1
	finishRow := func() {
		rows = append(rows, line.String())
		line.Reset()
		used = 0
	}
	for i := first; i < len(m.boundaries); i++ {
		display, hardBreak := "", false
		if i+1 < len(m.boundaries) {
			raw := m.text[m.boundaries[i]:m.boundaries[i+1]]
			raw, hardBreak = strings.CutSuffix(raw, "\n")
			if raw != "" {
				display = raw
				if len(raw) != 1 || raw[0] < ' ' || raw[0] > '~' {
					display = tokenizerSourceDisplay(raw)
				}
			}
		}
		if display == "" && i == cursor && m.focus == tokenizerFocusText {
			display = " "
		}
		cells := tokenizerSourceWidth(display)
		if cells > width {
			display, cells = "…", 1
		}
		if used+cells > width {
			finishRow()
		}
		if i == cursor {
			caretRow = len(rows)
			if m.focus == tokenizerFocusText {
				display = tokenizerCaret(display)
			}
		}
		line.WriteString(display)
		used += cells
		if hardBreak || i+1 == len(m.boundaries) {
			finishRow()
		}
		if caretRow >= 0 && len(rows) >= max(count, caretRow+1) {
			break
		}
	}
	start := max(0, caretRow-count+1)
	visible := make([]string, count)
	copy(visible, rows[start:])
	return visible
}
