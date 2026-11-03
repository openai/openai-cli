package audio

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type audioCommandClipboard struct {
	write   func(context.Context, string) (confirmed bool, err error)
	pending bool
	notice  string
}

func audioClipboardWriter(control func(string)) func(context.Context, string) (bool, error) {
	if runtime.GOOS == "darwin" && os.Getenv("SSH_TTY") == "" && os.Getenv("SSH_CONNECTION") == "" &&
		os.Getenv("SSH_CLIENT") == "" && os.Getenv("TMUX") == "" && os.Getenv("STY") == "" && os.Getenv("ZELLIJ") == "" {
		// Terminal.app does not require OSC 52 support when the pasteboard is local.
		return func(ctx context.Context, text string) (bool, error) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/pbcopy")
			cmd.Env = []string{"LC_ALL=en_US.UTF-8"}
			cmd.Stdin = strings.NewReader(text)
			err := cmd.Run()
			return err == nil, err
		}
	}
	// A remote terminal can deny clipboard writes without acknowledgement.
	return func(ctx context.Context, text string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		control(ansi.SetSystemClipboard(text))
		return false, nil
	}
}

func (m *audioWorkbench) copyCommand() tea.Cmd {
	if m.clipboard.pending || m.clipboard.write == nil {
		return nil
	}
	raw, ok := m.rawCommand()
	if !ok {
		return nil
	}
	m.clipboard.pending, m.clipboard.notice = true, "Copying…"
	write := m.clipboard.write
	return m.workers.command(m.ctx, func(ctx context.Context) audioWorkMessage {
		confirmed, err := write(ctx, raw)
		return audioWorkMessage{kind: "copy-command", clipboardConfirmed: confirmed, err: err}
	})
}
