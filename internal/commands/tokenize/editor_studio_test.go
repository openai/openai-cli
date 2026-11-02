package tokenize

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTokenSizeChartUsesByteLengthsAndAbsoluteTokenNumbers(t *testing.T) {
	m := testTokenizerEditor()
	const width = 44 // Twenty bars with fixed gaps and a two-cell inset.
	m.insert(strings.Repeat("x", 40) + "aébb")
	for i, r := range m.text {
		m.tokens = append(m.tokens, tokenizerPreviewToken{EndByte: uint32(i + utf8.RuneLen(r))})
	}
	m.selected = 41
	rows := m.tokenSizeChart(m.styles(), width)
	require.Equal(t, []string{"  Bytes per token  ▄[█]▄ ▄  41–44 · 2 B max"}, rows)
	m.tab = 2
	require.Equal(t, rows, m.tokenSizeChart(m.styles(), width))

	m.selected = 0
	rows = m.tokenSizeChart(m.styles(), width)
	require.Contains(t, rows[0], "1–20 · 1 B max")
	require.Equal(t, 43, ansi.StringWidth(rows[1]))
	require.Equal(t, "  [█]"+strings.Repeat("█ ", 19), rows[1])
}

func TestTokenSizeChartKeepsSelectionAndExactLengthsInFixedColumns(t *testing.T) {
	m := testTokenizerEditor()
	// Nine mixed lengths reproduce the short chart from the terminal capture.
	m.insert("ffdafasf asfasdfasf")
	for _, end := range []uint32{2, 5, 7, 8, 11, 14, 16, 18, 19} {
		m.tokens = append(m.tokens, tokenizerPreviewToken{EndByte: end})
	}
	const plain = "  Bytes per token  ▅2 █3 ▅2 ▃1 █3 █3 ▅2 ▅2 ▃1  3 B max"
	for _, theme := range []string{"dark", "light", "monochrome"} {
		t.Run(theme, func(t *testing.T) {
			m.color, m.dark = theme != "monochrome", theme == "dark"
			s := m.styles()
			for i, bar := range strings.Fields("▅2 █3 ▅2 ▃1 █3 █3 ▅2 ▅2 ▃1") {
				m.selected = i
				rows := m.tokenSizeChart(s, 61)
				require.Len(t, rows, 1)
				row := rows[0]
				wantColumn := len("  Bytes per token  ") + 3*i
				if m.color {
					require.Equal(t, plain, ansi.Strip(row))
					before, _, found := strings.Cut(row, s.accent.Render(bar))
					require.True(t, found)
					require.Equal(t, wantColumn, ansi.StringWidth(before))
					require.NotContains(t, row, "48;")
				} else {
					before, _, found := strings.Cut(row, "["+bar+"]")
					require.True(t, found)
					require.Equal(t, wantColumn, ansi.StringWidth(before)+1)
					require.Equal(t, plain, strings.NewReplacer("[", " ", "]", " ").Replace(row))
				}
				require.LessOrEqual(t, ansi.StringWidth(row), 61)
			}
		})
	}
}

func TestTokenSizeChartPreservesCompactHeightForEighteenTokens(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 133, 39
	m.insert(strings.Repeat("x", 18))
	for i := range len(m.text) {
		m.tokens = append(m.tokens, tokenizerPreviewToken{EndByte: uint32(i + 1)})
	}
	for _, color := range []bool{true, false} {
		m.color = color
		for i := range m.tokens {
			m.selected = i
			rows := m.tokenSizeChart(m.styles(), m.resultWidth())
			require.Len(t, rows, 1)
			require.Contains(t, rows[0], "1 B max")
			require.NotContains(t, rows[0], "1–18")
			require.Equal(t, 18, strings.Count(rows[0], "█"))
			require.LessOrEqual(t, ansi.StringWidth(rows[0]), m.resultWidth())
		}
	}
}

func TestTokenizerStudioOmitsChartUntilTokensAreReady(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 110, 28
	check := func(state string) {
		t.Helper()
		rows := m.studioResults(m.styles(), m.resultWidth())
		require.Len(t, rows, 1)
		require.Contains(t, rows[0], state)
		require.NotContains(t, rows[0], "Bytes per token")
	}
	check("○ Empty")
	m.insert("é")
	check("● Updating")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 2}}})
	rows := m.studioResults(m.styles(), m.resultWidth())
	require.Len(t, rows, 5)
	require.Contains(t, rows[0], "1 token")
	require.NotContains(t, rows[0], "Tokens  1 token")
	require.Contains(t, rows[0], "● Ready")
	require.Contains(t, rows[1], "é")
	require.Contains(t, rows[2], "Bytes per token")
	require.Contains(t, rows[2], "[█2]")
	require.Contains(t, rows[2], "2 B max")
	require.Contains(t, rows[3], "Token 1 of 1  “é”")
	require.Contains(t, rows[4], "ID 1  Byte offset 0  Length 2 B")

	m.insert("x")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision})
	check("● Failed")
}

func TestTokenizerStudioKeepsLongByteMetadataWithinPane(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height, m.tab = 110, 28, 2
	m.insert(strings.Repeat("a", 1<<20))
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{
		{ID: 1, EndByte: 100000}, {ID: 4294967295, EndByte: 1 << 20},
	}})
	m.selected = 1
	const width = 49
	rows := m.studioResults(m.styles(), width)
	for _, row := range rows {
		require.LessOrEqual(t, tokenizerSourceWidth(row), width)
	}
	text := strings.Join(rows, "\n")
	require.Contains(t, text, "2 tokens · 1048576 bytes")
	require.Contains(t, text, "ID 4294967295")
	require.Contains(t, text, "Byte offset 100000")
	require.Contains(t, text, "Length 948576 B")
	require.Contains(t, text, "948576 B max")
}

func TestTokenizerStudioShowsLastChartPageAtMinimumSize(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 96, 16
	m.insert(strings.Repeat("a", 1000) + "é")
	tokens := make([]tokenizerPreviewToken, len(m.text))
	for i := range tokens {
		tokens[i] = tokenizerPreviewToken{ID: 4294967295, EndByte: uint32(i + 1)}
	}
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: tokens})
	for tab := range 3 {
		m.tab = tab
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "[█1]")
		require.Contains(t, view, "· 1 B max")
		require.Contains(t, view, "Token 1002 of 1002 · partial UTF-8")
		require.Contains(t, view, "0xa9")
		require.Contains(t, view, "ID 4294967295")
		require.Contains(t, view, "Byte offset 1001")
		require.Contains(t, view, "Length 1 B")
		require.Contains(t, view, "Ctrl+C exit")
		for _, row := range strings.Split(view, "\n") {
			require.Less(t, ansi.StringWidth(row), m.width)
		}
	}
}
