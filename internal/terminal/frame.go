package terminal

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// InlineFrame renders content as one soft-wrapped logical terminal line.
func InlineFrame(content string, width int) string {
	var frame strings.Builder
	frame.WriteString("\r" + ansi.EraseScreenBelow)
	if content == "" || width <= 0 {
		return frame.String()
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		// A printable space completes the preceding row's pending wrap. CR
		// then returns to column zero without breaking that logical line.
		frame.WriteString(" \r")
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
		// Place the wrap boundary explicitly. Emoji clusters can occupy fewer
		// cells than wcwidth reports; spaces alone would leave the cursor on
		// the wrong row. The final cell is reserved from content above.
		frame.WriteString(ansi.CursorHorizontalAbsolute(width))
		frame.WriteByte(' ')
	}
	// CR cancels the pending wrap after the final boundary cell. All preceding
	// rows were reached by autowrap, so this remains one logical terminal line.
	frame.WriteByte('\r')
	if len(lines) > 1 {
		frame.WriteString(ansi.CursorUp(len(lines) - 1))
	}
	return frame.String()
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
