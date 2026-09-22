package terminalimage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePreviewDetection(t *testing.T) {
	for _, test := range []struct {
		name string
		tty  bool
		env  map[string]string
		want Protocol
	}{
		{"iTerm2", true, map[string]string{"TERM_PROGRAM": "iTerm.app"}, ITerm2},
		{"Ghostty", true, map[string]string{"TERM_PROGRAM": "ghostty"}, Kitty},
		{"Kitty", true, map[string]string{"TERM": "xterm-kitty"}, Kitty},
		{"Ghostty TERM over SSH", true, map[string]string{"TERM": "xterm-ghostty", "SSH_TTY": "/dev/pts/1"}, Kitty},
		{"forwarded identity over SSH", true, map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "ghostty", "SSH_CONNECTION": "synthetic"}, Kitty},
		{"pipe", false, map[string]string{"TERM_PROGRAM": "ghostty"}, ""},
		{"generic xterm", true, map[string]string{"TERM": "xterm-256color"}, ""},
		{"unknown identity", true, map[string]string{"TERM_PROGRAM": "vscode", "TERM": "xterm-kitty"}, ""},
		{"inherited session ID", true, map[string]string{"ITERM_SESSION_ID": "old", "KITTY_WINDOW_ID": "1"}, ""},
		{"dumb", true, map[string]string{"TERM": "dumb", "TERM_PROGRAM": "iTerm.app"}, ""},
		{"tmux variable", true, map[string]string{"TMUX": "/tmp/tmux", "TERM_PROGRAM": "ghostty"}, ""},
		{"tmux TERM", true, map[string]string{"TERM": "tmux-256color", "TERM_PROGRAM": "ghostty"}, ""},
		{"screen TERM", true, map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "iTerm.app"}, ""},
		{"screen variable", true, map[string]string{"STY": "1.screen", "TERM_PROGRAM": "iTerm.app"}, ""},
		{"zellij", true, map[string]string{"ZELLIJ": "0", "TERM_PROGRAM": "ghostty"}, ""},
		{"CI", true, map[string]string{"CI": "true", "TERM_PROGRAM": "ghostty"}, ""},
		{"CI false", true, map[string]string{"CI": "false", "TERM_PROGRAM": "ghostty"}, Kitty},
		{"NO_COLOR only disables colors", true, map[string]string{"NO_COLOR": "1", "TERM_PROGRAM": "ghostty"}, Kitty},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, DetectProtocol(test.tty, func(key string) string { return test.env[key] }))
		})
	}
}

func TestImagePreviewTrueColorDetection(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"Tahoe first build", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "465"}, true},
		{"Tahoe dotted build", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "470.2"}, true},
		{"older Apple", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "464.9"}, false},
		{"unknown Apple version", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false},
		{"malformed version", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "470.invalid"}, false},
		{"overflow version", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "999999999999999999999"}, false},
		{"different terminal build", map[string]string{"TERM_PROGRAM": "other", "TERM_PROGRAM_VERSION": "470.2", "TERM": "xterm-256color"}, false},
		{"explicit capability", map[string]string{"COLORTERM": "truecolor"}, true},
		{"explicit 24bit", map[string]string{"COLORTERM": "24bit"}, true},
		{"generic ANSI256", map[string]string{"TERM": "xterm-256color"}, false},
		{"no color", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "470.2", "NO_COLOR": "1"}, false},
		{"color disabled", map[string]string{"COLORTERM": "truecolor", "CLICOLOR": "0"}, false},
		{"dumb overrides capability", map[string]string{"COLORTERM": "truecolor", "TERM": "dumb"}, false},
		{"inherited Apple through tmux", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "470.2", "TMUX": "synthetic"}, false},
		{"screen term", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "470.2", "TERM": "screen-256color"}, false},
		{"multiplexer advertises RGB", map[string]string{"TERM": "tmux-256color", "COLORTERM": "truecolor", "TMUX": "synthetic"}, true},
		{"forwarded Apple over SSH", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "470.2", "SSH_TTY": "/dev/pts/1"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, TrueColor(func(key string) string { return test.env[key] }))
		})
	}
}
