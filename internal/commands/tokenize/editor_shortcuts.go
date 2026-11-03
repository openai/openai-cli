package tokenize

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	tokenizerPageHint = "PgUp/PgDn page"
	tokenizerExitHint = "Ctrl+C exit"
)

func (m *tokenizerEditor) footer(s tokenizerEditorStyles) []string {
	width := m.viewWidth()
	var navigation, actions, extra []string
	switch m.focus {
	case tokenizerFocusText:
		if m.sourceOverflows() {
			navigation = []string{tokenizerPageHint}
		}
		navigation = append(navigation, "Tab inspect tokens")
		actions = []string{"Shift+Tab settings"}
	case tokenizerFocusOptions:
		if m.option == 0 {
			navigation = []string{"←→ change view", "↑↓ move focus"}
		} else {
			navigation = []string{"↑↓ move focus", "Enter choose model"}
		}
		actions = []string{"Tab inspect tokens"}
	case tokenizerFocusResults:
		actions = []string{"Tab edit text"}
		if m.failed {
			navigation = []string{"r retry"}
		} else if len(m.tokens) > 0 {
			actions = []string{"Tab edit here"}
			extra = append(extra, "Enter open details")
			if len(m.tokens) > 1 {
				navigation = m.tokenNavigationHints(width)
			}
		}
		extra = append(extra, "Shift+Tab settings")
	}
	return s.shortcutFooter(width, navigation, actions, extra...)
}

func (m *tokenizerEditor) tokenNavigationHints(width int) []string {
	hints := []string{"←→ select token"}
	resultWidth := m.resultWidth()
	if m.walkResultSegments(0, resultWidth, 1, nil) < len(m.tokens) {
		hints = append(hints, "↑↓ move by row")
	}
	if m.resultEnd(0, resultWidth) < len(m.tokens) {
		hints = append(hints, tokenizerPageHint)
		if shortcutWidth(hints) > width {
			return []string{"Arrows select token", tokenizerPageHint}
		}
	}
	return hints
}

func shortcutWidth(hints []string) int {
	return ansi.StringWidth(strings.Join(hints, "  "))
}

// Keep navigation and actions together when they fit. On narrow terminals, each group gets one row.
func (s tokenizerEditorStyles) shortcutFooter(width int, navigation, actions []string, extra ...string) []string {
	actionWidth := shortcutWidth(append(actions, tokenizerExitHint))
	budget := width
	if len(navigation) > 0 {
		budget -= shortcutWidth(navigation) + 2
	}
	split := actionWidth > budget
	if split {
		budget = width
	}
	for _, hint := range extra {
		cells := 2 + ansi.StringWidth(hint)
		if actionWidth+cells <= budget {
			actions = append(actions, hint)
			actionWidth += cells
		}
	}
	actions = append(actions, tokenizerExitHint)
	if split {
		return []string{s.shortcutLine(navigation), s.shortcutLine(actions)}
	}
	return []string{s.shortcutLine(append(navigation, actions...))}
}

func (s tokenizerEditorStyles) shortcutLine(hints []string) string {
	pairs := make([]string, 0, len(hints))
	for _, hint := range hints {
		key, action, _ := strings.Cut(hint, " ")
		pairs = append(pairs, s.accent.Render(key)+" "+s.muted.Render(action))
	}
	return strings.Join(pairs, "  ")
}
