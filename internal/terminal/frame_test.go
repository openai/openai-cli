package terminal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
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

func TestHardLineFrameReservesTheRightEdgeAndReturnsToOrigin(t *testing.T) {
	start := "\r" + ansi.EraseScreenBelow
	style, reset := "\x1b[31m", "\x1b[m"
	content := style + "1️⃣aZ" + reset + "\n雪aX\n\ne\u0301\n"
	want := start + style + "1️⃣a" + reset + "\r\n雪a\r\n\r\ne\u0301\r\n\r" + ansi.CursorUp(4)
	require.Equal(t, want, HardLineFrame(content, 4))
	require.Equal(t, start+"\r\n\r"+ansi.CursorUp(1), HardLineFrame("x\ny", 1))
	require.Equal(t, start+"x\r", HardLineFrame("x", 2))
	require.Equal(t, start, HardLineFrame("x", 0))
	require.Equal(t, start, HardLineFrame("", 80))
}

func TestWrapProbeAndCursorReport(t *testing.T) {
	require.Empty(t, WrapProbe(2))
	require.Equal(t, ansi.SaveCursor+"\r"+ansi.EraseScreenBelow+ansi.CursorHorizontalAbsolute(80)+"  "+
		ansi.RequestCursorPositionReport+ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow, WrapProbe(80))
	for _, tt := range []struct {
		width, height, x, y int
		enabled, valid      bool
	}{
		{80, 24, 1, 23, true, true},
		{80, 24, 79, 0, false, true},
		{3, 2, 1, 0, true, true},
		{3, 2, 2, 1, false, true},
		{2, 2, 1, 1, false, false},
		{80, 1, 1, 0, false, false},
		{80, 24, 40, 0, false, false},
		{80, 24, 1, -1, false, false},
		{80, 24, 79, 24, false, false},
	} {
		enabled, valid := ObservedAutoWrap(tt.width, tt.height, tt.x, tt.y)
		require.Equal(t, tt.enabled, enabled, "%+v", tt)
		require.Equal(t, tt.valid, valid, "%+v", tt)
	}
}
