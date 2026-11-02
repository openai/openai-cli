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
	const width = 44 // Twenty tokens fit on the first chart page.
	m.insert(strings.Repeat("x", 22) + "éf")
	for i, r := range m.text {
		m.tokens = append(m.tokens, tokenizerPreviewToken{EndByte: uint32(i + utf8.RuneLen(r))})
	}
	m.selected = 22
	rows := m.tokenSizeChart(m.styles(), width)
	require.Contains(t, rows[0], "2 B max")
	require.Equal(t, "21", strings.Fields(rows[5])[0])
	require.Equal(t, "24", strings.Fields(rows[5])[1])
	// Four bars land at cells 0, 13, 26 and 39. Only é reaches the top row.
	require.Equal(t, 1, strings.Count(rows[1], "█"))
	require.Equal(t, '█', []rune(rows[1])[2+26])
	require.Equal(t, '●', []rune(rows[4])[2+26])
	m.tab = 2
	require.Equal(t, rows, m.tokenSizeChart(m.styles(), width))
}

func TestTokenizerStudioKeepsChartPositionAcrossWorkerStates(t *testing.T) {
	m := testTokenizerEditor()
	m.width, m.height = 110, 28
	chartLine := func(status string) int {
		frame := ansi.Strip(m.View().Content)
		require.Contains(t, frame, status)
		for i, row := range strings.Split(frame, "\n") {
			if strings.Contains(row, "Bytes per token") {
				return i
			}
		}
		t.Fatal("missing byte chart")
		return -1
	}
	line := chartLine("○ Empty")
	m.insert("é")
	require.Equal(t, line, chartLine("● Updating"))
	m.Update(tokenizerEditorResultMsg{Revision: m.revision, Tokens: []tokenizerPreviewToken{{ID: 1, EndByte: 2}}})
	require.Equal(t, line, chartLine("● Ready"))
	require.Contains(t, ansi.Strip(m.View().Content), "Byte length  2")
	m.insert("x")
	m.Update(tokenizerEditorResultMsg{Revision: m.revision})
	require.Equal(t, line, chartLine("● Failed"))
	require.NotContains(t, m.View().Content, "2 B max")
}
