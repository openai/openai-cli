package custom

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminalimage"
)

// displaySavedImage is shared by automatic previews and the local preview
// command. It never opens a viewer, reads stdin, or changes the saved file.
func displaySavedImage(ctx context.Context, path string, out, diagnostics io.Writer, mode string) error {
	protocol := savedImageProtocol(mode, isTerminal(out), os.Getenv)
	if protocol == "" {
		return nil
	}
	if diagnostics == nil {
		diagnostics = os.Stderr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	img, err := terminalimage.ReadSaved(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return readable.WriteText(diagnostics, "Inline preview unavailable. The image is saved; open the saved file to view it. No need to generate again.")
	}
	return displayDecodedSavedImage(ctx, img, out, diagnostics, protocol)
}

// displayDecodedSavedImage shares geometry, rendering and font recovery with
// local preview, whose caller reports invalid input before reaching this point.
func displayDecodedSavedImage(ctx context.Context, img image.Image, out, diagnostics io.Writer, protocol string) error {
	file, ok := out.(*os.File)
	if !ok || !isTerminal(out) {
		return errors.New("inline previews require a terminal")
	}
	if diagnostics == nil {
		diagnostics = os.Stderr
	}
	width, height, err := term.GetSize(file.Fd())
	if err != nil || width < 2 || height < 2 {
		return nil
	}
	columns := min(64, width-1)
	// Fit tall previews as well as wide ones. Native protocols retain all pixels.
	columns = min(columns, (height-2)*2*img.Bounds().Dx()/img.Bounds().Dy())
	if columns < 1 {
		return readable.WriteText(diagnostics, "Inline preview unavailable: the image is too tall for this terminal. Open the saved file to view it.")
	}
	if protocol == "font" {
		columns = min(columns, 32)
	}
	if protocol == "blocks" {
		if err := readable.WriteText(out, "Inline preview (color approximation):"); err != nil {
			return err
		}
	}
	err = terminalimage.Write(ctx, out, img, protocol, columns)
	var fontErr *terminalimage.FontError
	if errors.As(err, &fontErr) && ctx.Err() == nil {
		// A FontError guarantees no image glyphs were written. A block fallback is
		// safe and does not replace this tab's earlier immutable glyph assignments.
		if writeErr := readable.WriteText(diagnostics, fmt.Sprintf("Sharp inline preview unavailable: %s. The image is saved; no need to generate again.", fontErr.Error())); writeErr != nil {
			return writeErr
		}
		if savedImageBlockColor(os.Getenv) {
			if err := readable.WriteText(out, "Inline preview (color approximation):"); err != nil {
				return err
			}
			err = terminalimage.Write(ctx, out, img, "blocks", columns)
		} else {
			return nil
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out)
	return err
}

func savedImageProtocol(mode string, terminal bool, getenv func(string) string) string {
	if !terminal || mode == "off" {
		return ""
	}
	ci := strings.ToLower(getenv("CI"))
	t := getenv("TERM")
	if (ci != "" && ci != "0" && ci != "false") || t == "dumb" {
		return ""
	}
	mux := getenv("TMUX") != "" || getenv("STY") != "" || getenv("ZELLIJ") != "" || strings.HasPrefix(t, "screen") || strings.HasPrefix(t, "tmux")
	if !mux {
		switch getenv("TERM_PROGRAM") {
		case "kitty", "ghostty":
			return "kitty"
		case "iTerm.app", "WezTerm":
			return "iterm"
		case "Apple_Terminal":
			if mode == "on" && getenv("SSH_CONNECTION") == "" && getenv("SSH_CLIENT") == "" && getenv("SSH_TTY") == "" && runtime.GOOS == "darwin" {
				return "font"
			}
		case "":
			if t == "xterm-kitty" || t == "xterm-ghostty" {
				return "kitty"
			}
		}
	}
	if savedImageBlockColor(getenv) {
		return "blocks"
	}
	return ""
}

func savedImageBlockColor(getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" || getenv("CLICOLOR") == "0" {
		return false
	}
	color := strings.ToLower(getenv("COLORTERM"))
	return strings.Contains(getenv("TERM"), "256color") || color == "truecolor" || color == "24bit" || getenv("TERM_PROGRAM") == "Apple_Terminal"
}
