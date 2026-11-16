package terminal

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// InlineFrame renders content as one soft-wrapped logical terminal line.
func InlineFrame(content string, width int) string {
	return renderFrame(content, width, false)
}

// HardLineFrame uses explicit line breaks when the terminal's wrap mode is unknown.
func HardLineFrame(content string, width int) string {
	return renderFrame(content, width, true)
}

func renderFrame(content string, width int, hardBreaks bool) string {
	var frame strings.Builder
	frame.WriteString("\r" + ansi.EraseScreenBelow)
	if content == "" || width <= 0 {
		return frame.String()
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if hardBreaks {
			if i > 0 {
				frame.WriteString("\r\n")
			}
		} else {
			// A space completes a pending wrap. CR returns to column zero.
			frame.WriteString(" \r")
		}
		used, clipped := 0, false
		for len(line) > 0 {
			sequence, cells, read := Sequence(line)
			line = line[read:]
			if cells == 0 {
				frame.WriteString(sequence)
			} else if !clipped && used+cells < width {
				frame.WriteString(sequence)
				used += cells
			} else {
				clipped = true
			}
		}
		if !hardBreaks {
			// Emoji can occupy fewer cells than wcwidth reports. Place the wrap
			// boundary explicitly in the final cell reserved above.
			frame.WriteString(ansi.CursorHorizontalAbsolute(width))
			frame.WriteByte(' ')
		}
	}
	// Return to the frame origin. CR also cancels any pending soft wrap.
	frame.WriteByte('\r')
	if len(lines) > 1 {
		frame.WriteString(ansi.CursorUp(len(lines) - 1))
	}
	return frame.String()
}

// WrapProbe observes wrapping on the blank row owned by the upcoming frame.
// It restores the cursor and erases the probe before waiting for the reply.
func WrapProbe(width int) string {
	if width < 3 {
		return ""
	}
	return ansi.SaveCursor + "\r" + ansi.EraseScreenBelow +
		ansi.CursorHorizontalAbsolute(width) + "  " + ansi.RequestCursorPositionReport +
		ansi.RestoreCursor + "\r" + ansi.EraseScreenBelow
}

// ObservedAutoWrap interprets a zero-based cursor position reported after WrapProbe.
func ObservedAutoWrap(width, height, x, y int) (enabled, valid bool) {
	if width < 3 || height < 2 || y < 0 || y >= height {
		return false, false
	}
	// The second space reaches column 1 after wrapping, or stays at the right edge.
	return x == 1, x == 1 || x == width-1
}

// Sequence counts a complete printable cluster, including ASCII-led keycap emoji.
// text must be nonempty. ANSI's
// sequence decoder alone treats the leading digit as a separate byte. Taking
// the larger width covers both scalar and grapheme-aware terminal presentation.
func Sequence(text string) (sequence string, cells, read int) {
	if text[0] == '\x1b' {
		sequence, cells, read, _ = ansi.DecodeSequenceWc(text, 0, nil)
		return
	}
	sequence, cells = ansi.FirstGraphemeCluster(text, ansi.WcWidth)
	return sequence, max(cells, ansi.StringWidth(sequence)), len(sequence)
}

// FitPrompt clips and pads prompt text to the available terminal cells.
func FitPrompt(text string, width int) string {
	var result strings.Builder
	used := 0
	for len(text) > 0 {
		sequence, cells, read := Sequence(text)
		if used+cells > width || used+cells == width && read < len(text) {
			result.WriteRune('…')
			used++
			break
		}
		result.WriteString(sequence)
		used += cells
		text = text[read:]
	}
	result.WriteString(strings.Repeat(" ", max(0, width-used)))
	return result.String()
}
