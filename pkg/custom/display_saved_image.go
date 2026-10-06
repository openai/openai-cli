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
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminalimage"
)

var errImagePreviewUnavailable = errors.New("image cannot be displayed in this terminal")

// displaySavedImage previews only bytes from the completed save operation. It
// never opens a viewer, reads stdin, or changes the saved file.
func displaySavedImage(ctx context.Context, saved imageoutput.SavedImage, out, diagnostics io.Writer, mode string) error {
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
	img, err := terminalimage.ReadSavedMatching(ctx, saved.Path, saved.SHA256)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, terminalimage.ErrSavedImageChanged) {
			return readable.WriteText(diagnostics, "Inline preview skipped: the saved file changed before it could be displayed. Check the output file.")
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
	cellWidth, cellHeight := terminalimage.CellSize(file.Fd())
	columns := savedImagePreviewColumns(img.Bounds(), width, height, cellWidth, cellHeight)
	if protocol == "font" {
		columns = terminalimage.FontPreviewColumns(img.Bounds(), columns, height)
	}
	if columns < 1 && protocol != "iterm-auto" {
		return reportImagePreviewUnavailable(diagnostics, "Inline preview unavailable: the image is too tall for this terminal. Open the saved file to view it.")
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
			// Font preparation can outlive a window resize. Recheck the current
			// viewport before emitting the optional block fallback.
			width, height, sizeErr := term.GetSize(file.Fd())
			if sizeErr != nil {
				return errImagePreviewUnavailable
			}
			cellWidth, cellHeight := terminalimage.CellSize(file.Fd())
			columns = min(columns, savedImagePreviewColumns(img.Bounds(), width, height, cellWidth, cellHeight))
			if columns < 1 {
				return errImagePreviewUnavailable
			}
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

func savedImagePreviewColumns(bounds image.Rectangle, width, height, cellWidth, cellHeight int) int {
	if bounds.Empty() || width < 2 || height < 2 || cellWidth < 1 || cellHeight < 1 {
		return 0
	}
	// The renderer preserves image proportions using actual cell geometry when
	// available. Apply the same geometry here so native images fit vertically.
	// Keep exact fits exact. Decoded images and native window fields are bounded;
	// multiplying in int64 also avoids overflowing on 32-bit platforms.
	columns := min(int64(64), int64(width)-1)
	fit := (int64(height) - 2) * int64(cellHeight) * int64(bounds.Dx()) / (int64(cellWidth) * int64(bounds.Dy()))
	return int(min(columns, fit))
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
		case "WarpTerminal":
			// The CLI host does not identify Warp's platform in WSL or SSH.
			// Preserve the fallback when Windows may own the terminal.
			wsl := getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != ""
			ssh := getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != ""
			if wsl || ssh {
				break
			}
			// Warp documents native image protocols on macOS and Linux.
			if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
				return "kitty"
			}
		case "vscode":
			// VS Code does not export whether its optional image renderer is
			// active. This is an explicit user assertion, not autodetection.
			// Never query the terminal or inspect settings from this selector.
			if getenv("OPENAI_VSCODE_IMAGES") == "1" {
				return "iterm-auto"
			}
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
