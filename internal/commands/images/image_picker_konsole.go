package images

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/terminalimage"
)

// The picker keeps input, raw modes, and request ownership. Only explicit
// expansion uses a temporary screen; native images never enter its main panel.
func (w *imagePickerWorkspace) setKonsoleScreen(ctx context.Context, expanded bool) error {
	if expanded == w.konsoleScreen {
		return nil
	}
	var frame strings.Builder
	if expanded {
		if w.started && len(w.painted) > 0 {
			w.clearGraphics(&frame)
			frame.WriteString(ansi.RestoreCursor)
			// Remove the owned panel before resizing its hidden main buffer.
			// The shell transcript and the model remain intact.
			frame.WriteString(cliterminal.InlineFrame("", w.width))
		}
		frame.WriteString("\x1b[?1049h")
		// A partial entry still requires an attempted exit during cleanup.
		w.konsoleScreen = true
	} else {
		frame.WriteString("\x1b\\\x1b[?1049l")
	}
	w.painted, w.nativePainted = nil, nil
	w.frameRevision++
	w.originKnown = false
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err := terminalimage.WriteFrame(writeCtx, w.out, frame.String(), "iterm-konsole")
	if err == nil && !expanded {
		w.konsoleScreen = false
	}
	return err
}

// Konsole cannot remove or resize a retained native placement. Replace the
// temporary screen when image geometry changes; ordinary status updates only
// repaint their text rows. The existing cache still bounds retained PNG data.
func (w *imagePickerWorkspace) drawKonsoleFrame(ctx context.Context, lines []string, resized bool) error {
	repaint := resized || len(lines) != len(w.painted) || len(w.painted) == 0 || !maps.Equal(w.nativeFrame, w.nativePainted)
	keys, placements := w.nativePaintKeys(lines, repaint)
	var frame strings.Builder
	frame.WriteString(ansi.ResetModeTextCursorEnable)
	if repaint {
		frame.WriteString("\x1b[2J\x1b[H")
	}
	changed := repaint
	for row, line := range lines {
		if !repaint && keys[row] == w.painted[row] {
			continue
		}
		changed = true
		fmt.Fprintf(&frame, "\x1b[%d;1H\x1b[2K", row+1)
		if _, native := w.nativeFrame[row]; !native {
			line = ansi.Truncate(line, max(0, w.width-1), "…")
		}
		frame.WriteString(line)
	}
	if !changed {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := terminalimage.WriteFrame(writeCtx, w.out, frame.String(), "iterm-konsole"); err != nil {
		w.nativePainted = nil
		return err
	}
	w.painted, w.nativePainted = keys, placements
	w.started = true
	w.origin, w.originKnown = 0, true
	w.retainNativeFrame()
	return nil
}
