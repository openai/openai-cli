package tokenize

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Read complete key/action pairs from the rendered footer, including its continuation row.
func tokenizerVisibleShortcuts(content string) string {
	lines := strings.Split(ansi.Strip(content), "\n")
	if strings.HasPrefix(lines[len(lines)-1], "╰") {
		lines = lines[:len(lines)-1]
	}
	keys := []string{"Ctrl+C", "Tab", "Shift+Tab", "Enter", "Esc", "←→", "↑↓", "Arrows", "PgUp/PgDn", "r"}
	var rows []string
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.Trim(lines[i], " │")
		key, _, _ := strings.Cut(line, " ")
		if !slices.Contains(keys, key) {
			break
		}
		rows = append(rows, line)
	}
	slices.Reverse(rows)
	return strings.Join(rows, " · ")
}

func TestTokenizerFooterDescribesAvailableShortcuts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		count         int
		focus, option int
		want          string
	}{
		{"Text", 0, tokenizerFocusText, 0, "Tab inspect"},
		{"Paged Text", 12, tokenizerFocusText, 0, "Tab inspect"},
		{"View", 0, tokenizerFocusOptions, 0, "↑↓ focus · ←→ change view"},
		{"Model", 0, tokenizerFocusOptions, 1, "↑↓ focus · Enter choose model"},
		{"Empty tokens", 0, tokenizerFocusResults, 0, "Tab edit"},
		{"One token", 1, tokenizerFocusResults, 0, "Tab edit"},
		{"Two tokens", 2, tokenizerFocusResults, 0, "←→ select · Tab edit"},
		{"Wrapped tokens", 8, tokenizerFocusResults, 0, "←→ select · Tab edit"},
		{"Multiple pages", 80, tokenizerFocusResults, 0, "←→ select · Tab edit"},
	} {
		for _, size := range [][2]int{{40, 12}, {120, 30}} {
			t.Run(fmt.Sprintf("%s/%dx%d", tc.name, size[0], size[1]), func(t *testing.T) {
				m := testTokenizerEditor()
				m.width, m.height, m.color = size[0], size[1], true
				if tc.count > 0 {
					const fragment = "abcdefgh\n"
					m.Update(tea.PasteMsg{Content: strings.Repeat(fragment, tc.count)})
					tokens := make([]tokenizerPreviewToken, tc.count)
					for i := range tokens {
						tokens[i].EndByte = uint32((i + 1) * len(fragment))
					}
					m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
				}
				m.focus, m.option = tc.focus, tc.option
				require.Equal(t, tc.want+" · Ctrl+C exit", tokenizerVisibleShortcuts(m.View().Content))
			})
		}
	}
}

func TestTokenizerChooserAndDetailsShortcuts(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {120, 30}} {
		for _, tc := range []struct {
			name string
			key  rune
			want string
		}{
			{"Choose view", 'v', "↑↓ choose · Enter apply · Esc cancel · Ctrl+C exit"},
			{"Choose model", 'm', "↑↓ choose · Enter apply · Esc cancel · Ctrl+C exit"},
			{"Details", 'd', "Esc back · Ctrl+C exit"},
		} {
			t.Run(fmt.Sprintf("%s/%dx%d", tc.name, size[0], size[1]), func(t *testing.T) {
				m := tokenizerEditorExample()
				m.width, m.height, m.focus = size[0], size[1], tokenizerFocusResults
				tokenizerEditorKey(m, tc.key)
				require.Equal(t, tc.want, tokenizerVisibleShortcuts(m.View().Content))
			})
		}
	}
}
