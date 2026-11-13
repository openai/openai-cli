package audio

import (
	"bytes"
	"context"
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

// Translate body coordinates only after the final row budget is known.
func (m *audioWorkbench) panelCursor(cursor *tea.Cursor, bodyHeight int) *tea.Cursor {
	if cursor == nil || m.busy || m.quit || !m.usable() {
		return nil
	}
	if cursor.X < 0 || cursor.X >= m.audioWidth() || cursor.Y < 0 || cursor.Y >= bodyHeight {
		return nil
	}
	position := *cursor
	if m.framed() {
		position.X++
		position.Y++
	} else {
		position.X += 2
	}
	return &position
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
