package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"runtime"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
)

// FontSupported identifies a local Apple Terminal session. Font registration
// and tab control must never target a remote shell or a terminal multiplexer.
func FontSupported() bool {
	return imagefontmac.Supported() && localAppleImageTerminal(runtime.GOOS, os.Getenv)
}

type fontServices = imageFontServices

// FontError means preparing a sharp preview failed before any image characters
// were written. The caller can still display a fallback without duplicating output.
type FontError struct{ Err error }

func (e *FontError) Error() string { return e.Err.Error() }
func (e *FontError) Unwrap() error { return e.Err }

func writeImageFont(ctx context.Context, out io.Writer, img image.Image, columns int) error {
	file, ok := out.(*os.File)
	if !ok || !FontSupported() {
		return &FontError{errors.New("sharp image fonts require a local Apple Terminal tab")}
	}
	tty, err := imageFontTTY(ctx, file)
	if err != nil {
		return &FontError{err}
	}
	directory, err := imageFontDirectory(ctx, out)
	if err != nil {
		return &FontError{err}
	}
	cleanupSessionFontGalleries(ctx, directory, tty)
	services := nativeImageFontServices()
	return displayNativeImageFont(ctx, out, img, columns, directory, tty, func() fontViewport {
		return readFontViewport(file.Fd())
	}, services)
}

func displayNativeImageFont(ctx context.Context, out io.Writer, img image.Image, columns int, directory, tty string, viewport func() fontViewport, services fontServices) (err error) {
	writing := false
	defer func() {
		if err != nil && !writing {
			err = &FontError{err}
		}
	}()
	gallery, err := imagegallery.Open(ctx, directory)
	if err != nil {
		return err
	}
	defer gallery.Close()
	if err := bindImageFontSession(ctx, gallery, directory, tty); err != nil {
		return err
	}
	initial, err := gallery.Initialize(ctx)
	if err != nil {
		return err
	}
	// Commit the empty gallery before native calls so a denied Automation
	// request can be retried without leaving orphaned cache state.
	if err := gallery.Commit(ctx, initial); err != nil {
		return err
	}
	state := gallery.State()
	before, err := services.snapshot(ctx, state.ProfileName, tty)
	if err != nil {
		return err
	}
	if before.FontSize != float64(int(before.FontSize)) {
		return errors.New("sharp previews require a whole-number Terminal font size")
	}
	// Restore an existing immutable font registration after a logout before
	// asking CoreText to resolve its original text face.
	if path, err := gallery.LookupFontPS(ctx, before.FontName); err != nil {
		return err
	} else if path != "" {
		if err := registerFont(ctx, services, path, true); err != nil {
			return err
		}
	}
	source, err := services.source(ctx, before.FontName, int(before.FontSize))
	if err != nil {
		return err
	}
	size := viewport()
	geometry, err := preservedGeometry(size, int(before.FontSize), source)
	if err != nil {
		return err
	}
	if columns < 1 {
		columns = 32
	}
	columns = min(columns, 32)
	if size.Columns > 0 {
		columns = min(columns, size.Columns-1)
	}
	if columns < 1 {
		return errors.New("widen Terminal before displaying the image")
	}
	revision, err := gallery.PrepareImage(ctx, img, columns)
	if err != nil {
		if errors.Is(err, imagegallery.ErrFull) {
			return errors.New("this tab's image font is full; open a new Terminal tab to continue displaying sharp images")
		}
		return err
	}
	if size.Columns > 0 && revision.Columns >= size.Columns {
		return fmt.Errorf("widen Terminal to at least %d columns to display this cached image", revision.Columns+1)
	}
	writing, err = activateImageFontRevision(ctx, out, gallery, revision, tty, size, before, source, geometry, viewport, services)
	return err
}

func registerFont(ctx context.Context, services fontServices, path string, existing bool) error {
	return registerImageFont(ctx, services, path, existing)
}
