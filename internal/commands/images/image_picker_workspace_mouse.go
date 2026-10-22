package images

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminalimage"
)

const imagePickerMouseQuery = "\x1b[?1000$p\x1b[?1002$p\x1b[?1003$p\x1b[?1006$p"

// Generic unknown and inherited tracking remain untouched.
// A native cooked Apple Terminal workspace owns a mouse-disabled shell baseline.
type imagePickerMouseModes struct {
	reports                     [4]ansi.ModeSetting
	tracking, encoding, stopped bool
	shell                       bool
}

func (m *imagePickerMouseModes) report(msg tea.ModeReportMsg) bool {
	for i, mode := range []ansi.DECMode{1000, 1002, 1003, 1006} {
		if msg.Mode == mode {
			if !m.tracking && !m.stopped && !m.shell {
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
	if m.shell {
		return imagePickerMouseShellReset
	}
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
	return imagePickerDisplayFailure(terminalimage.WriteFrame(ctx, w.out, frame, ""))
}

// The native-shell contract excludes remote, multiplexed, and unknown callers.
func imagePickerMouseShellEnvironment(getenv func(string) string) bool {
	if getenv("TERM_PROGRAM") != "Apple_Terminal" || getenv("KITTY_WINDOW_ID") != "" {
		return false
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ"} {
		if getenv(key) != "" {
			return false
		}
	}
	ci := strings.ToLower(getenv("CI"))
	if ci != "" && ci != "0" && ci != "false" {
		return false
	}
	term := getenv("TERM")
	return term != "" && term != "dumb" && !strings.HasPrefix(term, "screen") && !strings.HasPrefix(term, "tmux")
}

const imagePickerMouseShellReset = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l"

func (m *imagePickerMouseModes) openShell() string {
	if m.stopped || m.shell || m.tracking {
		return ""
	}
	// Record ownership before writing. Cleanup must cover a partial reset.
	m.shell = true
	m.reports = [4]ansi.ModeSetting{ansi.ModeReset, ansi.ModeReset, ansi.ModeReset, ansi.ModeReset}
	return imagePickerMouseShellReset
}
