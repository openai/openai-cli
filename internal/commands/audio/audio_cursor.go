package audio

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Bubbles owns Unicode wrapping and viewport offsets; Audio adds layout offsets.
func audioEditorCursor(editor textarea.Model, x, y int) *tea.Cursor {
	cursor := editor.Cursor()
	if cursor == nil || cursor.X < 0 || cursor.X >= editor.Width() || cursor.Y < 0 || cursor.Y >= editor.Height() {
		return nil
	}
	cursor.X += x
	cursor.Y += y
	cursor.Shape, cursor.Blink = tea.CursorBar, true
	return cursor
}

// Coordinates refer to the unframed form rows before optional gaps are removed.
func (m *audioWorkbench) formCursor(compact bool) *tea.Cursor {
	if m.modal != "" || m.busy || m.quit || !m.usable() {
		return nil
	}
	textHeight := 1
	if !compact || m.focus == 0 {
		textHeight = m.input.Height()
	}
	x, gap := 2, 0
	if !compact {
		x, gap = m.formLabelWidth()+3, 1
		textHeight++ // The full-width composer keeps its heading above the editor.
	}
	switch m.focus {
	case 0:
		if !compact {
			return audioEditorCursor(m.input, 2, 1)
		}
		return audioEditorCursor(m.input, x, 0)
	case 1:
		if !m.instructionsSupported() {
			return nil
		}
		y := textHeight + gap
		if compact {
			y++ // Compact Instructions keeps its label above the editor.
		}
		return audioEditorCursor(m.instructions, x, y)
	case 2:
		if m.setting == 3 && m.speedEditing {
			instructionsHeight := 1
			if !compact && !m.instructionsSupported() && m.instructions.Value() != "" {
				instructionsHeight++
			}
			// Model, Voice, and Format precede Speed. Framed rows also have
			// two section gaps.
			y := textHeight + instructionsHeight + 3 + 2*gap
			return audioEditorCursor(m.speedInput, m.formLabelWidth()+3, y)
		}
	}
	return nil
}

// Translate body coordinates only after the final row budget is known.
func (m *audioWorkbench) cursorView(lines []string, footerCount int, cursor *tea.Cursor) tea.View {
	view := tea.NewView(m.fitAudio(lines, footerCount))
	if cursor == nil || m.busy || m.quit || !m.usable() {
		return view
	}
	rows := 0
	for _, line := range lines {
		rows += strings.Count(line, "\n") + 1
	}
	bodyHeight := min(rows, m.audioHeight())
	if m.framed() {
		footerCount = min(footerCount, rows, m.audioHeight())
		bodyHeight = min(rows-footerCount, m.audioHeight()-footerCount)
	}
	if cursor.X < 0 || cursor.X >= m.audioWidth() || cursor.Y < 0 || cursor.Y >= bodyHeight {
		return view
	}
	if m.framed() {
		cursor.X++
		cursor.Y++
	} else {
		cursor.X += 2
	}
	view.Cursor = cursor
	return view
}

// A queued first frame can be discarded during cancellation. Restore its saved
// origin only after the output worker acknowledges bytes containing SaveCursor.
type audioCursorOutput struct {
	output   cliterminal.FrameOutput
	anchored atomic.Bool
}

func (w *audioCursorOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	n, err := w.output.WriteContext(ctx, data)
	if n > 0 && bytes.Contains(data[:min(n, len(data))], []byte(ansi.SaveCursor)) {
		w.anchored.Store(true)
	}
	return n, err
}
