package tokenize

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTokenizerStudioSourceWrapsWithoutChangingDraft(t *testing.T) {
	m := testTokenizerEditor()
	source := "hello, world\n\n界e\u0301\r\n\tx!"
	m.insert(source)
	rows := m.studioSourceLines(m.styles(), 6, 6)
	require.Equal(t, []string{"hello,", " world", "", "界e\u0301" + tokenizerSourceDisplay("\r"), `\tx!` + tokenizerCaret(" "), ""}, rows)
	m.cursor = len("hello,")
	rows = m.studioSourceLines(m.styles(), 6, 6)
	require.Equal(t, tokenizerCaret(" ")+"world", rows[1])
	m.cursor = len("hello, world\n\n界")
	rows = m.studioSourceLines(m.styles(), 6, 6)
	require.Contains(t, rows[3], tokenizerCaret("e\u0301"), "the caret covers the entire combining cluster")
	require.Equal(t, source, m.text)
}

func TestTokenizerStudioSourceKeepsOversizedGraphemeAndEndCaretVisible(t *testing.T) {
	for _, source := range []string{strings.Repeat("0123456789", 100000), "z" + strings.Repeat("\u0301", 1<<18)} {
		m := testTokenizerEditor()
		m.insert(source)
		for _, index := range []int{0, len(m.boundaries) / 2, len(m.boundaries) - 1} {
			m.cursor = m.boundaries[index]
			rows := m.studioSourceLines(m.styles(), 20, 4)
			require.Len(t, rows, 4)
			require.Equal(t, 1, strings.Count(strings.Join(rows, ""), "\x1b[7m"))
			for _, row := range rows {
				require.LessOrEqual(t, tokenizerSourceWidth(ansi.Strip(row)), 20)
			}
			if m.cursor == len(source) {
				require.Contains(t, strings.Join(rows, ""), tokenizerCaret(" "))
			}
		}
		require.Equal(t, source, m.text)
	}
}
