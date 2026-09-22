package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Keep the caller's selected text face, point size, and profile settings. An
// image-capable local copy retains the original font's outlines and metrics. No
// profile is imported, selected, renamed, or saved; no window is opened.
func setupCurrentImageFont(ctx context.Context, out io.Writer, dir, tty string, services imageFontServices) error {
	gallery, err := prepareImageFontGallery(ctx, dir, services)
	if err != nil {
		return err
	}
	defer gallery.Close()
	state := gallery.State()
	if services.snapshot == nil || services.source == nil || services.preserve == nil {
		return errors.New("font-preserving Terminal setup is unavailable")
	}
	before, err := services.snapshot(ctx, state.ProfileName, tty)
	if err != nil {
		return err
	}
	if before.FontSize != float64(int(before.FontSize)) {
		return errors.New("choose a whole-number Terminal font size before enabling sharp previews")
	}
	if err := restoreSelectedImageFont(ctx, gallery, before.FontName, services); err != nil {
		return err
	}
	source, err := services.source(ctx, before.FontName, int(before.FontSize))
	if err != nil {
		return err
	}
	var size Size
	if file, ok := out.(*os.File); ok && isTerminal(file) {
		size = TerminalSize(file.Fd())
	}
	geometry, err := imageFontPreservedGeometry(size, int(before.FontSize), source)
	if err != nil {
		return err
	}
	revision, err := gallery.Initialize(ctx)
	if err != nil {
		return err
	}
	companions, err := imageFontCompanionGeometry(size, int(before.FontSize), source)
	if err != nil {
		return err
	}
	display, err := gallery.FontForTypography(ctx, revision, geometry, companions...)
	if err != nil {
		return err
	}
	if err := registerTypographyFont(ctx, services, display); err != nil {
		return err
	}
	if err := services.preserve(ctx, state.ProfileName, tty, display.PostScript, before); err != nil {
		return fmt.Errorf("enable sharp previews in this tab: %w", err)
	}
	_, err = fmt.Fprintf(out, "Keeping font: %q at %g pt.\nSharp previews enabled in this tab. Your text style, font size, profile and colors are kept.\n", source.PostScript, before.FontSize)
	return err
}
