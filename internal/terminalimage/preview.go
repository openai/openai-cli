package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
)

// ReportFailure explains a preview failure after an image was successfully saved.
func ReportFailure(out io.Writer, previewErr error) error {
	message := "Preview unavailable; open the saved image to view it."
	var fontErr *imageFontPreviewError
	if errors.As(previewErr, &fontErr) {
		message = fontErr.Error() + "\nThe generated image is saved; no new generation is needed."
	}
	_, err := fmt.Fprintln(out, message)
	return err
}

// Read geometry after generation so resizing while waiting is respected.
// The local preview command uses this same path without another API request.
func Preview(ctx context.Context, out io.Writer, path string, protocol Protocol, textColor, trueColor bool) error {
	var size Size
	if file, ok := out.(*os.File); ok {
		size = TerminalSize(file.Fd())
	}
	if protocol == "" {
		if handled, err := tryImageFontPreview(ctx, out, path, size); handled {
			return err
		}
		if _, err := fmt.Fprintln(out, "Inline preview (text approximation):"); err != nil {
			return err
		}
		if err := RenderText(ctx, out, path, size, textColor, trueColor); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Use --open for full resolution in a separate window, or Ghostty/iTerm2 for a native inline image.")
		return err
	}
	return Render(ctx, out, path, protocol, size)
}

// Recognize terminal identities without queries or reads from stdin.
// Multiplexers need passthrough handling: inherited terminal identities do not
// prove that graphics will reach the outer terminal safely.
func DetectProtocol(terminal bool, getenv func(string) string) Protocol {
	if !terminal {
		return ""
	}
	if InCI(getenv) {
		return ""
	}
	t := getenv("TERM")
	if t == "dumb" || strings.HasPrefix(t, "screen") || strings.HasPrefix(t, "tmux") ||
		getenv("TMUX") != "" || getenv("STY") != "" || getenv("ZELLIJ") != "" {
		return ""
	}
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return ITerm2
	case "ghostty":
		return Kitty
	case "": // Useful over SSH when TERM_PROGRAM was not forwarded.
	default:
		return "" // An explicitly different terminal takes precedence.
	}
	if t == "xterm-kitty" || t == "xterm-ghostty" {
		return Kitty
	}
	return ""
}

func InCI(getenv func(string) string) bool {
	ci := strings.ToLower(getenv("CI"))
	return ci != "" && ci != "false" && ci != "0"
}

// Basic terminals still get ASCII. Use color only when advertised, without
// queries or input reads; TrueColor selects RGB where supported.
func TextColor(getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" || getenv("CLICOLOR") == "0" || getenv("TERM") == "dumb" {
		return false
	}
	color := strings.ToLower(getenv("COLORTERM"))
	return strings.Contains(getenv("TERM"), "256color") || color == "truecolor" || color == "24bit" || getenv("TERM_PROGRAM") == "Apple_Terminal"
}

// Tahoe added RGB color to Apple Terminal (2.15, build 465). Inspect the
// terminal's advertised build, not the CLI host OS, so SSH remains correct.
// https://ratatui.rs/examples/layout/flex/
func TrueColor(getenv func(string) string) bool {
	if !TextColor(getenv) {
		return false
	}
	color := strings.ToLower(getenv("COLORTERM"))
	if color == "truecolor" || color == "24bit" {
		return true
	}
	term := getenv("TERM")
	if getenv("TERM_PROGRAM") != "Apple_Terminal" ||
		getenv("TMUX") != "" || getenv("STY") != "" || getenv("ZELLIJ") != "" ||
		strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") {
		return false
	}
	parts := strings.Split(getenv("TERM_PROGRAM_VERSION"), ".")
	for _, part := range parts {
		if part == "" || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) != -1 {
			return false
		}
	}
	build, err := strconv.Atoi(parts[0])
	return err == nil && build >= 465
}

func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}
