package tokenize

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	tokenizerShortcutSeparator = " · "
	tokenizerPageHint          = "PgUp/PgDn page"
	tokenizerExitHint          = "Ctrl+C exit"
)

func (m *tokenizerEditor) footer(s tokenizerEditorStyles) []string {
	var navigation, actions []string
	switch m.focus {
	case tokenizerFocusText:
		actions = []string{"Tab inspect"}
	case tokenizerFocusOptions:
		navigation = []string{"↑↓ focus"}
		if m.option == 0 {
			actions = []string{"←→ change view"}
		} else {
			actions = []string{"Enter choose model"}
		}
	case tokenizerFocusResults:
		actions = []string{"Tab edit"}
		if m.failed {
			navigation = []string{"r retry"}
		} else if len(m.tokens) > 1 {
			navigation = []string{"←→ select"}
		}
	}
	return s.shortcutFooter(m.viewWidth(), navigation, actions)
}

func shortcutWidth(hints []string) int {
	return ansi.StringWidth(strings.Join(hints, tokenizerShortcutSeparator))
}

// Keep navigation and actions together when they fit. On narrow terminals, each group gets one row.
func (s tokenizerEditorStyles) shortcutFooter(width int, navigation, actions []string) []string {
	actions = append(actions, tokenizerExitHint)
	hints := append(navigation, actions...)
	if len(navigation) != 0 && shortcutWidth(hints) > width {
		return []string{s.shortcutLine(navigation), s.shortcutLine(actions)}
	}
	return []string{s.shortcutLine(hints)}
}

func (s tokenizerEditorStyles) shortcutLine(hints []string) string {
	pairs := make([]string, 0, len(hints))
	for _, hint := range hints {
		key, action, _ := strings.Cut(hint, " ")
		pairs = append(pairs, s.accent.Render(key)+" "+s.muted.Render(action))
	}
	return strings.Join(pairs, s.muted.Render(tokenizerShortcutSeparator))
}
