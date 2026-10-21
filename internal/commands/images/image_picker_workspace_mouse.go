package images

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminalimage"
)

const imagePickerMouseQuery = "\x1b[?1000$p\x1b[?1002$p\x1b[?1003$p\x1b[?1006$p"

// Unknown and inherited tracking remain untouched. Keyboard navigation always works.
type imagePickerMouseModes struct {
	reports                     [4]ansi.ModeSetting
	tracking, encoding, stopped bool
}

func (m *imagePickerMouseModes) report(msg tea.ModeReportMsg) bool {
	for i, mode := range []ansi.DECMode{1000, 1002, 1003, 1006} {
		if msg.Mode == mode {
			if !m.tracking && !m.stopped {
				m.reports[i] = msg.Value
			}
			return true
		}
	}
	return false
}

func (m *imagePickerMouseModes) enable() string {
	if m.stopped || m.tracking || m.reports[0] != ansi.ModeReset || !m.reports[1].IsReset() || !m.reports[2].IsReset() {
		return ""
	}
	var output string
	switch m.reports[3] {
	case ansi.ModeReset:
		output = "\x1b[?1006h"
		m.encoding = true
	case ansi.ModeSet, ansi.ModePermanentlySet:
	default:
		return ""
	}
	m.tracking = true
	return output + "\x1b[?1000h"
}

func (m *imagePickerMouseModes) disable() string {
	m.stopped = true
	var output string
	if m.tracking {
		output = "\x1b[?1000l"
	}
	if m.encoding {
		output += "\x1b[?1006l"
	}
	return output
}

// Stop reporting before waiting for workers or restoring terminal input.
// Repeated cleanup retries the same owned resets after a previous write failure.
func (w *imagePickerWorkspace) stopMouse() error {
	frame := w.mouse.disable()
	if frame == "" {
		return nil
	}
	parent := w.context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 250*time.Millisecond)
	defer cancel()
	return terminalimage.WriteFrame(ctx, w.out, frame, "")
}
