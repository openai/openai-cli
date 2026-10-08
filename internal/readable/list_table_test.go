package readable

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestListTableKeepsIDsAndFitsWidth(t *testing.T) {
	headers := []string{"ID", "FILENAME", "PURPOSE", "SIZE", "STATUS"}
	rows := [][]string{{"file-example-1", "training-東京-👩🏽‍💻-" + strings.Repeat("x", 100) + ".jsonl", "fine-tune", "1 KiB", "processed"}, {"file-example-2", "short.jsonl", "batch", "0 B", "uploaded"}}
	for _, width := range []int{58, 80, 110} {
		text, ok := ListTable(headers, rows, width)
		require.True(t, ok)
		require.Contains(t, text, "file-example-1")
		require.Contains(t, text, "file-example-2")
		require.Contains(t, text, "…")
		for _, line := range strings.Split(text, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), width, "%q", line)
		}
		require.Len(t, strings.Split(strings.TrimSuffix(text, "\n"), "\n"), 3)
	}
	require.Contains(t, rows[0][1], strings.Repeat("x", 100), "input must stay unchanged")
}

func TestListTableNarrowFallbackKeepsIDs(t *testing.T) {
	for _, width := range []int{-1, 0, 1, 40} {
		text, ok := ListTable([]string{"ID", "NAME"}, [][]string{{"proj-" + strings.Repeat("a", 60), "東京"}}, width)
		require.False(t, ok)
		require.Empty(t, text)
	}
}

func TestListTableEscapesEveryCell(t *testing.T) {
	text, ok := ListTable([]string{"ID", "NAME"}, [][]string{{"file-\x1b]52;c;a\a", "a\nb\tc\r\u202e\u009b"}}, 110)
	require.True(t, ok)
	require.Contains(t, text, `file-\u001b]52;c;a\u0007`)
	require.Contains(t, text, `a\nb\tc\r\u202e\u009b`)
	require.NotContains(t, text, "\x1b")
	require.NotContains(t, text, "\r")
	require.Len(t, strings.Split(strings.TrimSuffix(text, "\n"), "\n"), 2)
}

func TestListTableUnicodeWidths(t *testing.T) {
	rows := [][]string{{"模型-👩🏽‍💻", "Cafe\u0301 東京"}, {"model-2", "大阪"}}
	text, ok := ListTable([]string{"ID", "OWNER"}, rows, 30)
	require.True(t, ok)
	for _, row := range rows {
		for _, cell := range row {
			require.Contains(t, text, cell)
		}
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for _, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), 30)
	}
	a := strings.Index(lines[1], "Cafe")
	b := strings.Index(lines[2], "大阪")
	require.Equal(t, ansi.StringWidth(lines[1][:a]), ansi.StringWidth(lines[2][:b]))
}

func TestListTableEmptyAndMalformedRows(t *testing.T) {
	text, ok := ListTable([]string{"ID"}, nil, 80)
	require.True(t, ok)
	require.Equal(t, "No results.\n", text)
	_, ok = ListTable(nil, nil, 80)
	require.False(t, ok)
	_, ok = ListTable([]string{"ID", "NAME"}, [][]string{{"id"}}, 80)
	require.False(t, ok)
}
