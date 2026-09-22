package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
)

// Setup enables sharp previews in the current Apple Terminal tab and displays
// a local sample, preserving the selected profile and text typography.
func Setup(ctx context.Context, out io.Writer) error {
	if err := checkAppleTerminal(); err != nil {
		return err
	}
	if !isTerminal(out) {
		return errors.New("run setup directly in the Apple Terminal tab you want to use")
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	services := nativeImageFontServices()
	file := out.(*os.File)
	tty, err := imageFontTTY(ctx, file)
	if err != nil {
		return err
	}
	if err := setupCurrentImageFont(ctx, file, dir, tty, services); err != nil {
		return err
	}
	return runImageInlineTest(ctx, file, dir, tty, TerminalSize(file.Fd()), services)
}

// Status reports gallery usage. check also verifies the current tab's font;
// details includes cache paths and exact capacity.
func Status(ctx context.Context, out io.Writer, check, details bool) error {
	if check {
		if err := checkAppleTerminal(); err != nil {
			return err
		}
	} else if runtime.GOOS != "darwin" {
		_, err := fmt.Fprintln(out, "Change the preference with: openai images inline on | off")
		return err
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(dir, "state.json")); errors.Is(err, os.ErrNotExist) {
		if check {
			return errors.New("run openai images inline setup before checking this tab's font")
		}
		_, err = fmt.Fprintln(out, "Not set up. Run: openai images inline setup")
		return err
	}
	if err != nil {
		return err
	}
	gallery, err := imagegallery.OpenForReset(ctx, dir)
	if err != nil {
		return err
	}
	defer gallery.Close()
	state := gallery.State()
	usage, err := gallery.Usage()
	if err != nil {
		return err
	}
	if check {
		if !isTerminal(out) {
			return errors.New("tab font verification requires terminal output")
		}
		if usage.MissingFiles != 0 {
			return errors.New("some preview cache files are missing; run openai images inline repair, or reset if thumbnails are missing")
		}
		services := nativeImageFontServices()
		if err := registerImageFont(ctx, services, state.FontPath, true); err != nil {
			return err
		}
		tty, err := imageFontTTY(ctx, out.(*os.File))
		if err != nil {
			return err
		}
		status, err := services.inspect(ctx, state.ProfileName, tty)
		if err != nil {
			return err
		}
		if err := restoreSelectedImageFont(ctx, gallery, status.FontName, services); err != nil {
			return err
		}
		textFace, err := services.source(ctx, status.FontName, int(status.FontSize))
		if errors.Is(err, imagefontmac.ErrLegacyFont) {
			if _, err := fmt.Fprintln(out, "Text uses an older fixed-font preview. Select your preferred font and size, then run: openai images inline setup"); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if _, err := fmt.Fprintf(out, "Text font: %q at %g pt.\n", textFace.PostScript, status.FontSize); err != nil {
			return err
		}
		size := TerminalSize(out.(*os.File).Fd())
		if err := checkImageFontWidth(state, size); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Image font at %.0fpt checked; window width is sufficient.\nSpacing needs a visual check: openai images inline test\n", status.FontSize); err != nil {
			return err
		}
	}
	return printImageFontStatus(out, state, usage, dir, details)
}

// Reset removes owned preview artifacts after confirming that no tab uses them.
// Saved originals and the automatic-preview preference are unaffected.
func Reset(ctx context.Context, out io.Writer) error {
	if err := checkAppleTerminal(); err != nil {
		return err
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	services := nativeImageFontServices()
	return resetImageFontCache(ctx, out, dir, services)
}

// Test displays a deterministic sample using the current gallery and font.
// It makes no API request and removes its temporary source image afterward.
func Test(ctx context.Context, out io.Writer) error {
	if err := checkAppleTerminal(); err != nil {
		return err
	}
	if !isTerminal(out) {
		return errors.New("run the visual check directly in an Apple Terminal tab with image previews enabled")
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	file := out.(*os.File)
	tty, err := imageFontTTY(ctx, file)
	if err != nil {
		return err
	}
	return runImageInlineTest(ctx, file, dir, tty, TerminalSize(file.Fd()), nativeImageFontServices())
}

func checkAppleTerminal() error {
	if !imagefontmac.Supported() || !localAppleImageTerminal(runtime.GOOS, os.Getenv) {
		return errors.New("run this command in Apple Terminal directly on your Mac, outside SSH or a terminal multiplexer")
	}
	return nil
}
