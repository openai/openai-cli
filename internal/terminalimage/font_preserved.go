package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
)

func displayPreservedImageFont(ctx context.Context, out io.Writer, gallery *imagegallery.Gallery, path, tty string, size Size, before imagefontmac.ProfileStatus, source imagefontmac.SourceFont, services imageFontServices) error {
	if services.preserve == nil {
		return errors.New("font-preserving Terminal activation is unavailable")
	}
	geometry, err := imageFontPreservedGeometry(size, int(before.FontSize), source)
	if err != nil {
		return err
	}
	columns := 32
	if size.Columns > 0 {
		columns = min(columns, size.Columns-1)
	}
	if columns < 8 {
		return errors.New("widen the terminal to at least 9 columns, then preview the saved file again")
	}
	revision, err := gallery.Prepare(ctx, path, columns)
	if err != nil {
		return err
	}
	if size.Columns > 0 && revision.Columns >= size.Columns {
		return fmt.Errorf("widen the terminal to at least %d columns to view this cached image", revision.Columns+1)
	}
	_, err = activateImageFontRevision(ctx, out, gallery, revision, tty, size, before, source, geometry, func() Size {
		if file, ok := out.(*os.File); ok && isTerminal(file) {
			return TerminalSize(file.Fd())
		}
		return size
	}, services)
	return err
}

// Both in-memory images and saved previews use the same activation transaction.
// On failure before output, restore the captured settings only if the user has
// not changed them concurrently. Once output starts its glyph font must remain.
func activateImageFontRevision(ctx context.Context, out io.Writer, gallery *imagegallery.Gallery, revision *imagegallery.Revision, tty string, size Size, before imagefontmac.ProfileStatus, source imagefontmac.SourceFont, geometry imagefont.PreserveOptions, viewport func() Size, services imageFontServices) (writing bool, err error) {
	companions, err := imageFontCompanionGeometry(size, int(before.FontSize), source)
	if err != nil {
		return false, err
	}
	display, err := gallery.FontForTypography(ctx, revision, geometry, companions...)
	if err != nil {
		return false, err
	}
	if err := registerTypographyFont(ctx, services, display); err != nil {
		return false, err
	}
	state := gallery.State()
	defer func() {
		if err != nil && !writing && services.restore != nil {
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err = errors.Join(err, services.restore(restoreCtx, state.ProfileName, tty, display.PostScript, before))
		}
	}()
	if err := services.preserve(ctx, state.ProfileName, tty, display.PostScript, before); err != nil {
		return false, err
	}
	after, err := services.inspect(ctx, state.ProfileName, tty)
	if err != nil {
		return false, err
	}
	current := viewport()
	final, err := imageFontPreservedGeometry(current, int(after.FontSize), source)
	if err != nil || after.FontSize != before.FontSize || after.FontName != display.PostScript || after.ProfileID != before.ProfileID || after.ProfileName != before.ProfileName || final.CellWidth != geometry.CellWidth || final.CellHeight != geometry.CellHeight {
		return false, errors.New("Terminal font or spacing changed while preparing the preview; retry the saved image")
	}
	if current.Columns > 0 && revision.Columns >= current.Columns {
		return false, fmt.Errorf("widen Terminal to at least %d columns to view this cached image", revision.Columns+1)
	}
	if err := gallery.Commit(ctx, revision); err != nil {
		return false, err
	}
	writing = true
	_, err = io.WriteString(contextWriter{ctx, out}, display.Text)
	return writing, err
}

func restoreSelectedImageFont(ctx context.Context, gallery *imagegallery.Gallery, selected string, services imageFontServices) error {
	if selected == gallery.State().PostScript {
		return nil
	} // already registered
	path, err := gallery.LookupFontPS(ctx, selected)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	return registerImageFont(ctx, services, path, true)
}

func imageFontCompanionGeometry(size Size, pointSize int, source imagefontmac.SourceFont) ([]imagefont.PreserveOptions, error) {
	var companions []imagefont.PreserveOptions
	for _, face := range source.Companions {
		geometry, err := imageFontPreservedGeometry(size, pointSize, face)
		if err != nil {
			return nil, err
		}
		companions = append(companions, geometry)
	}
	return companions, nil
}

func registerTypographyFont(ctx context.Context, services imageFontServices, display imagegallery.TypographyFont) error {
	// Register the whole local family before selecting it so normal, bold and
	// italic text resolve to the user's actual outlines instead of synthesis.
	for _, face := range append(display.Related, display.DisplayFont) {
		if err := registerImageFont(ctx, services, face.FontPath, face.Existing); err != nil {
			return err
		}
	}
	return nil
}
