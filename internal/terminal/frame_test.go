package terminal

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestImagePickerInlineFrameHasOneLogicalLine(t *testing.T) {
	for _, width := range []int{1, 7, 40, 80, 120} {
		content := strings.Repeat("❤️", 10) + "\n\x1b[31m" + strings.Repeat("1️⃣", 10) + "\x1b[m\n雪 e\u0301 👨‍👩‍👧‍👦"
		frame := InlineFrame(content, width)
		require.NotContains(t, frame, "\n", "hard line breaks lose ownership during reflow")
		require.True(t, strings.HasPrefix(frame, "\r"+ansi.EraseScreenBelow))
		require.True(t, strings.HasSuffix(frame, "\r"+ansi.CursorUp(2)))
		paint := strings.TrimSuffix(strings.TrimPrefix(frame, "\r"+ansi.EraseScreenBelow), "\r"+ansi.CursorUp(2))
		rows := strings.Split(paint, ansi.CursorHorizontalAbsolute(width)+" ")
		require.Len(t, rows, 4, "three explicit right-edge wrap boundaries")
		require.Empty(t, rows[3])
		for _, row := range rows[:3] {
			require.True(t, strings.HasPrefix(row, " \r"))
			require.Less(t, ansi.WcWidth.StringWidth(strings.TrimPrefix(row, " \r")), width)
			require.Less(t, ansi.StringWidth(strings.TrimPrefix(row, " \r")), width)
		}
	}
	require.Equal(t, "\r"+ansi.EraseScreenBelow, InlineFrame("", 80))
}

func TestImagePickerWidthUsesWholeEmojiClusters(t *testing.T) {
	for _, test := range []struct {
		text  string
		cells int
	}{{"1️⃣", 2}, {"❤️", 2}, {"👨‍👩‍👧‍👦", 8}, {"雪", 2}, {"e\u0301", 1}} {
		sequence, cells, read := Sequence(test.text + "next")
		require.Equal(t, test.text, sequence)
		require.Equal(t, test.cells, cells, test.text)
		require.Equal(t, len(test.text), read)
	}
}
