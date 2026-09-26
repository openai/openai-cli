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

var errImagePreviewUnavailable = errors.New("image cannot be displayed in this terminal")

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
	err = displayDecodedSavedImage(ctx, img, out, diagnostics, protocol)
	if errors.Is(err, errImagePreviewUnavailable) {
		return nil
	}
	return err
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
		return reportImagePreviewUnavailable(diagnostics, "Inline preview unavailable: widen the terminal and retry the saved image.")
	}
	// Fit tall previews as well as wide ones. Native protocols retain all pixels.
	columns := savedImagePreviewColumns(img.Bounds(), width, height)
	if columns < 1 {
		return reportImagePreviewUnavailable(diagnostics, "Inline preview unavailable: the image is too tall for this terminal. Open the saved file to view it.")
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
		if writeErr := reportImageFontUnavailable(diagnostics, fontErr); writeErr != nil {
			return writeErr
		}
		if savedImageBlockColor(os.Getenv) {
			if err := readable.WriteText(out, "Inline preview (color approximation):"); err != nil {
				return err
			}
			err = terminalimage.Write(ctx, out, img, "blocks", columns)
		} else {
			return errImagePreviewUnavailable
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out)
	return err
}

func reportImageFontUnavailable(out io.Writer, err error) error {
	message := err.Error()
	var pathErr *os.PathError
	var linkErr *os.LinkError
	if errors.As(err, &pathErr) || errors.As(err, &linkErr) {
		// Wrapped or joined filesystem errors can expose several cache paths.
		// Keep those details out of diagnostics, including any wrapper text.
		message = "image font files could not be accessed; open the saved file to view it"
	}
	return readable.WriteText(out, fmt.Sprintf("Sharp inline preview unavailable: %s. The image is saved; no need to generate again.", message))
}

func savedImagePreviewColumns(bounds image.Rectangle, width, height int) int {
	if bounds.Empty() || width < 2 || height < 2 {
		return 0
	}
	// ReadSaved bounds image dimensions and terminal sizes are bounded too, but
	// multiplying them can overflow a 32-bit int. Clamp before converting back.
	columns := min(int64(64), int64(width)-1)
	return int(min(columns, (int64(height)-2)*2*int64(bounds.Dx())/int64(bounds.Dy())))
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

func reportImagePreviewUnavailable(out io.Writer, message string) error {
	if err := readable.WriteText(out, message); err != nil {
		return err
	}
	return errImagePreviewUnavailable
}
