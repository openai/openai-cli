package tokenize

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

type tokenizerRowFixture struct {
	text string
	id   uint32
}

func tokenizerRowEditor(width, height, tab int, pieces []tokenizerRowFixture) *tokenizerEditor {
	m := testTokenizerEditor()
	m.width, m.height, m.tab = width, height, tab
	var source strings.Builder
	var tokens []tokenizerPreviewToken
	for _, p := range pieces {
		source.WriteString(p.text)
		tokens = append(tokens, tokenizerPreviewToken{ID: p.id, EndByte: uint32(source.Len())})
	}
	m.Update(tea.PasteMsg{Content: source.String()})
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	tokenizerEditorKey(m, tea.KeyTab)
	tokenizerEditorKey(m, tea.KeyHome)
	return m
}

func TestTokenizerVerticalArrowsFollowDisplayedTokenColumns(t *testing.T) {
	pieces := []tokenizerRowFixture{
		{"aaaa", 8}, {"界界界界界界", 1234}, {"cc", 50}, {strings.Repeat("d", 17), 12345},
		{"eeeeeeeee", 111}, {"fff", 93}, {"ggggggggggg", 123}, {"hhhhhhh", 70000},
		{"i", 8}, {"jjjjjjjjjjjj", 1111}, {"kkkk", 12345}, {"lllll", 44}, {"mm", 555},
		{"nnnnnnnnnnn", 7}, {"oooo", 42}, {"ppp", 349}, {"qqqqqqqq", 8888}, {"r", 90},
		{strings.Repeat("s", 17), 9},
	}
	for _, tc := range []struct {
		name             string
		tab, start, down int
	}{
		// Text token 3 spans cells 20–36. Its middle lies over token 9 on the next row.
		{"Text", 0, 3, 9},
		// ID 1111 sits beyond every ID on the short second row. Down reaches its last token.
		{"Token IDs", 1, 9, 18},
		{"Bytes", 2, 1, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tokenizerRowEditor(96, 16, tc.tab, pieces)
			for range tc.start {
				tokenizerEditorKey(m, tea.KeyRight)
			}
			check := func(want, row int) {
				t.Helper()
				require.Equal(t, tokenizerFocusResults, m.focus)
				require.Nil(t, m.View().Cursor)
				require.Equal(t, want, m.selected)
				s := m.styles()
				width := m.resultWidth()
				label := m.tokenSegment(want, width)
				require.Contains(t, m.resultLines(s, width)[row], s.tokenSelected.Render(label))
			}
			tokenizerEditorKey(m, tea.KeyUp)
			check(tc.start, 0)
			tokenizerEditorKey(m, tea.KeyDown)
			check(tc.down, 1)
			if tc.tab == 1 {
				tokenizerEditorKey(m, tea.KeyDown)
				check(tc.down, 1)
			}
			tokenizerEditorKey(m, tea.KeyUp)
			check(tc.start, 0)

			offset := 0
			for _, p := range pieces[:tc.start] {
				offset += len(p.text)
			}
			draft, revision := m.text, m.revision
			tokenizerEditorKey(m, tea.KeyTab)
			require.Equal(t, revision, m.revision)
			require.Equal(t, tokenizerFocusText, m.focus)
			require.Equal(t, offset, m.cursor)
			require.NotNil(t, m.View().Cursor)
			m.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
			require.Equal(t, draft[:offset]+"!"+draft[offset:], m.text)
		})
	}
}

func TestTokenizerVerticalArrowsScrollOneRowInCompactMode(t *testing.T) {
	// Four eight-cell tokens fit on each full row. The last row has two tokens.
	pieces := make([]tokenizerRowFixture, 10)
	for i := range pieces {
		pieces[i].text = strings.Repeat(string(rune('a'+i)), 8)
	}
	m := tokenizerRowEditor(40, 12, 0, pieces)
	tokenizerEditorKey(m, tea.KeyRight)
	tokenizerEditorKey(m, tea.KeyRight)
	for _, step := range []struct {
		key   rune
		index int
	}{
		{tea.KeyUp, 2}, {tea.KeyDown, 6}, {tea.KeyDown, 9}, {tea.KeyDown, 9},
		{tea.KeyUp, 6}, {tea.KeyUp, 2}, {tea.KeyUp, 2},
		{tea.KeyDown, 6}, {tea.KeyDown, 9}, {tea.KeyLeft, 8}, {tea.KeyUp, 4}, {tea.KeyUp, 0},
	} {
		tokenizerEditorKey(m, step.key)
		require.Equal(t, tokenizerFocusResults, m.focus)
		require.Equal(t, step.index, m.selected)
		rows := m.resultLines(m.styles(), m.resultWidth())
		require.Len(t, rows, 1)
		require.Contains(t, rows[0], m.styles().tokenSelected.Render(pieces[step.index].text))
	}
}

func TestTokenizerVerticalArrowsReturnToThePreviouslyDisplayedRow(t *testing.T) {
	var pieces []tokenizerRowFixture
	for i, width := range []int{25, 7, 10, 9, 18, 12, 13, 13} {
		pieces = append(pieces, tokenizerRowFixture{text: strings.Repeat(string(rune('a'+i)), width)})
	}
	m := tokenizerRowEditor(40, 12, 0, pieces)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, 3, m.selected)
	row := m.resultLines(m.styles(), m.resultWidth())[0]
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, 4, m.selected)
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, 6, m.selected)
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, 4, m.selected)
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, 3, m.selected, "column 14 pointed to the fourth token on the preceding row")
	require.Equal(t, row, m.resultLines(m.styles(), m.resultWidth())[0])
}

func TestTokenizerVerticalArrowsCrossTheEdgeOfAMultirowPage(t *testing.T) {
	pieces := make([]tokenizerRowFixture, 30)
	for i := range pieces {
		pieces[i].text = strings.Repeat("x", 8)
	}
	// The 49-cell strip fits five tokens per row and four rows per page.
	m := tokenizerRowEditor(96, 16, 0, pieces)
	for range 18 {
		tokenizerEditorKey(m, tea.KeyRight)
	}
	tokenizerEditorKey(m, tea.KeyDown)
	require.Equal(t, 23, m.selected)
	tokenizerEditorKey(m, tea.KeyUp)
	require.Equal(t, 18, m.selected)
	require.Equal(t, tokenizerFocusResults, m.focus)
}
