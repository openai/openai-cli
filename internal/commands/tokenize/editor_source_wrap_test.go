package tokenize

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTokenizerStudioSourceWrapsWithoutChangingDraft(t *testing.T) {
	m := testTokenizerEditor()
	source := "hello, world\n\n界e\u0301\r\n\tx!"
	m.insert(source)
	rows := m.studioSourceLines(m.styles(), 6, 6)
	require.Equal(t, []string{"hello,", " world", "", "界e\u0301" + tokenizerSourceDisplay("\r"), `\tx!` + tokenizerCaret(" "), ""}, rows)
	m.cursor = len("hello")
	tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
	rows = m.studioSourceLines(m.styles(), 6, 6)
	require.Equal(t, tokenizerCaret(" ")+"world", rows[1])
	m.cursor = len("hello, world\n\n界e\u0301")
	tokenizerEditorModifiedKey(m, tea.KeyLeft, tea.ModShift)
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

func TestTokenizerStudioPageKeepsWrappedParagraphContiguous(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 96, 16
	var draft strings.Builder
	for i := range 255 {
		fmt.Fprintf(&draft, "%02x%c", i, 0x4e00+i)
	}
	source := draft.String()
	m.insert(source)
	revision := m.revision
	_, indent := m.sourceLabel()
	width, count := m.sourceWidth()-indent, m.sourceRows()
	require.True(t, m.sourceOverflows())

	for _, direction := range []int{1, -1} {
		key, home := tea.KeyPgDown, tea.KeyHome
		if direction < 0 {
			key, home = tea.KeyPgUp, tea.KeyEnd
		}
		tokenizerEditorModifiedKey(m, home, tea.ModCtrl)
		var previousStart, previousEnd int
		for step := range len(m.boundaries) {
			rows := m.studioSourceLines(m.styles(), width, count)
			visible := strings.TrimSuffix(ansi.Strip(strings.Join(rows, "")), " ")
			start := strings.Index(source, visible)
			require.GreaterOrEqual(t, start, 0, "page must contain a continuous substring")
			end := start + len(visible)
			if step > 0 {
				if direction > 0 {
					require.Equal(t, previousEnd, start, "the next page starts at the first unseen grapheme")
				} else {
					require.Less(t, start, previousStart)
					require.GreaterOrEqual(t, end, previousStart, "backward paging cannot skip source")
				}
			}
			if direction > 0 && end == len(source) || direction < 0 && start == 0 {
				break
			}
			previousStart, previousEnd = start, end
			oldCursor := m.cursor
			require.Nil(t, tokenizerEditorKey(m, key))
			require.Greater(t, (m.cursor-oldCursor)*direction, 0)
			require.Contains(t, m.boundaries, m.cursor)
			require.Equal(t, tokenizerFocusText, m.focus)
			require.Equal(t, revision, m.revision)
			require.Equal(t, source, m.text)
		}
	}
}

func TestTokenizerStudioPageMeasuresEscapedTextAndEndCaret(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 120, 30
	_, indent := m.sourceLabel()
	width, count := m.sourceWidth()-indent, m.sourceRows()
	row := "e\u0301界\t" + strings.Repeat("a", width-5)
	m.insert(strings.Repeat(row, count*2))
	tokenizerEditorModifiedKey(m, tea.KeyHome, tea.ModCtrl)
	tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
	tokenizerEditorModifiedKey(m, tea.KeyRight, tea.ModShift)
	tokenizerEditorKey(m, tea.KeyPgDown)
	require.Equal(t, count*len(row)+len("e\u0301界"), m.cursor)
	visible := m.studioSourceLines(m.styles(), width, count)
	require.Contains(t, visible[0], tokenizerCaret(`\t`))
	tokenizerEditorKey(m, tea.KeyPgDown)
	require.Equal(t, len(m.text), m.cursor)
	require.Contains(t, m.studioSourceLines(m.styles(), width, count)[0], tokenizerCaret(" "))
	tokenizerEditorKey(m, tea.KeyPgUp)
	require.GreaterOrEqual(t, m.cursor, count*len(row))
	require.Less(t, m.cursor, (count+1)*len(row), "a reconstructed page overlaps by at most one row")
	for range 2 {
		tokenizerEditorKey(m, tea.KeyPgUp)
	}
	require.Zero(t, m.cursor)

	m.insert("x")
	require.Contains(t, m.studioSourceLines(m.styles(), width, count)[0], "x"+tokenizerCaret("e\u0301"))
}

func TestTokenizerStudioPagingKeepsWidthAcrossLineNumbers(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 96, 16
	m.insert(strings.Repeat("abc\n", 12))
	m.cursor = m.lineStarts[8] + 1
	first, width := m.sourceLabel()
	tokenizerEditorKey(m, tea.KeyPgDown)
	last, nextWidth := m.sourceLabel()
	require.Equal(t, "Text 9/13", first)
	require.Equal(t, "Text 12/13", last)
	require.Equal(t, width, nextWidth)
	require.Equal(t, m.lineStarts[11]+1, m.cursor)
}
