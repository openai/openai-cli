package tokenize

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// The source window follows the editing caret or the graphemes touched by the inspected token.
type tokenizerSourceRange struct {
	first, end int
}

func (m *tokenizerEditor) sourceRange() tokenizerSourceRange {
	if m.focus == tokenizerFocusResults && len(m.tokens) > 0 && !m.failed && !m.updating {
		_, start, end := m.fragment(m.selected)
		return tokenizerSourceRange{sort.SearchInts(m.boundaries, start+1) - 1, sort.SearchInts(m.boundaries, end)}
	}
	caret := m.cursorIndex()
	return tokenizerSourceRange{caret, caret}
}

func (r tokenizerSourceRange) contains(index int) bool { return index >= r.first && index < r.end }

func tokenizerSourceCell(raw string, selected bool) (string, bool) {
	display, newline := strings.CutSuffix(raw, "\n")
	if display != "" && (len(display) != 1 || display[0] < ' ' || display[0] > '~') {
		display = tokenizerSourceDisplay(display)
	}
	if newline && selected {
		display += "↵"
	}
	return display, newline
}

// Record the visible part of the selection after escaping controls and clipping cells.
// Apply the style once per row so adjacent graphemes form one continuous highlight.
type tokenizerSourceRow struct {
	strings.Builder
	start, end int
}

func (r *tokenizerSourceRow) appendCell(display string, selected bool) {
	if selected && r.end == 0 {
		r.start = r.Len()
	}
	r.WriteString(display)
	if selected {
		r.end = r.Len()
	}
}

func (r *tokenizerSourceRow) render(style lipgloss.Style) string {
	text := r.String()
	if r.end <= r.start {
		return text
	}
	return text[:r.start] + style.Render(text[r.start:r.end]) + text[r.end:]
}
