package images

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/urfave/cli/v3"
)

type imagePickerDiagnosticOutputKey struct{}
type imagePickerDiagnosticWriter struct{ output *cliterminal.Output }

func (w imagePickerDiagnosticWriter) Write(data []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return w.output.WriteContext(ctx, data)
}

func imagePickerDiagnostics(ctx context.Context) io.Writer {
	if output, _ := ctx.Value(imagePickerDiagnosticOutputKey{}).(*cliterminal.Output); output != nil {
		return imagePickerDiagnosticWriter{output}
	}
	return os.Stderr
}

// A quiet cancellation must not hide a joined restoration or output failure.
// Keep the complete cause available to the shared diagnostic presenter.
type imagePickerReportError struct{ error }

func (e *imagePickerReportError) Unwrap() error { return e.error }
func (e *imagePickerReportError) ExitCode() int { return 1 }

func imagePickerReportFailure(failure error) error {
	var exit cli.ExitCoder
	if failure != nil && errors.As(failure, &exit) && exit.Error() == "" && strings.TrimSpace(failure.Error()) != "" {
		return &imagePickerReportError{failure}
	}
	return failure
}

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

// Expand fits the complete image. One PNG preserves a single scale across all
// pixels; independently fitted source strips can leave gaps and unequal widths.
func imagePickerKonsolePreview(ctx context.Context, _ *os.File, img image.Image, protocol string, columns int) ([]string, string, error) {
	if columns < 1 {
		return nil, protocol, errors.New("invalid image viewport")
	}
	var frame strings.Builder
	if err := terminalimage.Write(ctx, &frame, img, protocol, columns); err != nil {
		return nil, protocol, err
	}
	return []string{frame.String()}, protocol, nil
}

// Konsole cannot remove or resize a retained native placement. Replace the
// temporary screen when image geometry changes; ordinary status updates only
// repaint their text rows. The existing cache still bounds retained PNG data.
func (w *imagePickerWorkspace) drawKonsoleFrame(ctx context.Context, lines []string, resized bool) error {
	repaint := resized || len(lines) != len(w.painted) || len(w.painted) == 0 || !maps.Equal(w.nativeFrame, w.nativePainted)
	keys, placements := w.nativePaintKeys(lines, repaint)
	var frame strings.Builder
	var imageRows []int
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
		if _, native := w.nativeFrame[row]; native {
			// Clear continuation rows before placing the complete image.
			imageRows = append(imageRows, row)
		} else {
			frame.WriteString(ansi.Truncate(line, max(0, w.width-1), "…"))
		}
	}
	if !changed {
		return nil
	}
	parts := []string{frame.String()}
	native := false
	for _, row := range imageRows {
		parts = append(parts, fmt.Sprintf("\x1b[%d;5H", row+1), lines[row])
		native = native || strings.HasPrefix(lines[row], "\x1b]1337;")
	}
	if native {
		return w.startKonsoleTransfer(ctx, parts, keys, placements)
	}
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := terminalimage.WriteFrame(writeCtx, w.out, strings.Join(parts, ""), "iterm-konsole"); err != nil {
		w.nativePainted = nil
		return err
	}
	w.painted, w.nativePainted = keys, placements
	w.started = true
	w.origin, w.originKnown = 0, true
	w.retainNativeFrame()
	return nil
}
