package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
)

// FontSupported identifies a local Apple Terminal session. Font registration
// and tab control must never target a remote shell or a terminal multiplexer.
func FontSupported() bool {
	if !imagefontmac.Supported() || os.Getenv("TERM_PROGRAM") != "Apple_Terminal" {
		return false
	}
	ci := strings.ToLower(os.Getenv("CI"))
	if ci != "" && ci != "0" && ci != "false" {
		return false
	}
	for _, name := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ"} {
		if os.Getenv(name) != "" {
			return false
		}
	}
	return true
}

type fontServices struct {
	snapshot func(context.Context, string, string) (imagefontmac.ProfileStatus, error)
	source   func(context.Context, string, int) (imagefontmac.SourceFont, error)
	register func(context.Context, string) error
	preserve func(context.Context, string, string, string, imagefontmac.ProfileStatus) error
	restore  func(context.Context, string, string, string, imagefontmac.ProfileStatus) error
	inspect  func(context.Context, string, string) (imagefontmac.ProfileStatus, error)
}

// FontError means preparing a sharp preview failed before any image characters
// were written. The caller can still display a fallback without duplicating output.
type FontError struct{ Err error }

func (e *FontError) Error() string { return e.Err.Error() }
func (e *FontError) Unwrap() error { return e.Err }

func writeImageFont(ctx context.Context, out io.Writer, img image.Image, columns int) error {
	if strings.TrimSpace(os.Getenv("TERM_SESSION_ID")) == "" {
		return &FontError{errors.New("sharp previews require TERM_SESSION_ID; open a new Apple Terminal tab and retry the saved image")}
	}
	file, directory, tty, err := currentFontSession(ctx, out)
	if err != nil {
		return &FontError{err}
	}
	services := nativeFontServices()
	if err := displayImageFont(ctx, out, img, columns, directory, tty, func() fontViewport {
		return readFontViewport(file.Fd())
	}, services); err != nil {
		return err
	}
	// Clean up only after the selected font has been validated and rendered.
	// A changed session identity must never discard the previous gallery.
	// Cleanup remains best-effort and cannot fail a completed preview.
	_ = cleanupClosedFontGalleries(ctx, directory, tty)
	return nil
}

func displayImageFont(ctx context.Context, out io.Writer, img image.Image, columns int, directory, tty string, viewport func() fontViewport, services fontServices) (err error) {
	writing := false
	defer func() {
		if err != nil && !writing {
			err = &FontError{err}
		}
	}()
	gallery, err := openFontGallery(ctx, directory, tty)
	if err != nil {
		return err
	}
	defer gallery.Close()
	before, source, err := currentFontSource(ctx, gallery, tty, services)
	if err != nil {
		return err
	}
	size := viewport()
	// Reject unsupported text geometry before decoding or caching the image.
	if _, err := preservedGeometry(size, int(before.FontSize), source); err != nil {
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
	revision, err := gallery.Prepare(ctx, img, columns)
	if err != nil {
		if errors.Is(err, imagegallery.ErrFull) {
			return errors.New("this tab's image font is full; open a new Terminal tab to continue displaying sharp images")
		}
		return err
	}
	display, err := activateImageFont(ctx, gallery, revision, revision.Columns, tty, before, source, size, viewport, services)
	if err != nil {
		return err
	}
	writing = true
	_, err = io.WriteString(contextWriter{ctx, out}, display.Text)
	return err
}

// openFontGallery keeps the renderer and explicit repair on the same ownership
// and pending-attempt transaction. No closed-session cleanup is performed here.
func openFontGallery(ctx context.Context, directory, tty string) (*imagegallery.Gallery, error) {
	gallery, err := imagegallery.Open(ctx, directory)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*imagegallery.Gallery, error) { _ = gallery.Close(); return nil, err }
	if err := gallery.BindTTY(ctx, tty); err != nil {
		return fail(err)
	}
	initial, err := gallery.Initialize(ctx)
	if err != nil {
		return fail(err)
	}
	// Retain a stable identity even when Automation permission is denied.
	if err := gallery.Commit(ctx, initial); err != nil {
		return fail(err)
	}
	return gallery, nil
}

func currentFontSource(ctx context.Context, gallery *imagegallery.Gallery, tty string, services fontServices) (imagefontmac.ProfileStatus, imagefontmac.SourceFont, error) {
	before, err := services.snapshot(ctx, gallery.State().ProfileName, tty)
	fail := func(err error) (imagefontmac.ProfileStatus, imagefontmac.SourceFont, error) {
		return before, imagefontmac.SourceFont{}, err
	}
	if err != nil {
		return fail(err)
	}
	if before.FontSize != float64(int(before.FontSize)) {
		return fail(errors.New("sharp previews require a whole-number Terminal font size"))
	}
	// Restore login-session registration before resolving the original text face.
	if path, err := gallery.LookupFontPS(ctx, before.FontName); err != nil {
		return fail(err)
	} else if path != "" {
		if err := registerFont(ctx, services, path, true); err != nil {
			return fail(err)
		}
	} else if isImageFont(before.FontName) {
		return fail(errors.New("this tab's image font cannot be matched to the current session; open a new Terminal tab to keep earlier previews intact"))
	}
	source, err := services.source(ctx, before.FontName, int(before.FontSize))
	return before, source, err
}

func isImageFont(name string) bool {
	return strings.HasPrefix(name, "OpenAIImages-") || strings.HasPrefix(name, "OpenAI Local ") || strings.HasPrefix(name, "OpenAI Image Gallery ")
}

// activateImageFont commits only after native inspection confirms unchanged
// text settings. Failed or canceled activation gets conditional rollback, while
// registered fonts remain retained for any uncertain native result.
func activateImageFont(ctx context.Context, gallery *imagegallery.Gallery, revision *imagegallery.Revision, columns int, tty string, before imagefontmac.ProfileStatus, source imagefontmac.SourceFont, size fontViewport, viewport func() fontViewport, services fontServices) (_ imagegallery.TypographyFont, err error) {
	state := gallery.State()
	geometry, err := preservedGeometry(size, int(before.FontSize), source)
	if err != nil {
		return imagegallery.TypographyFont{}, err
	}
	if size.Columns > 0 && columns >= size.Columns {
		return imagegallery.TypographyFont{}, fmt.Errorf("widen Terminal to at least %d columns to display this cached image", columns+1)
	}
	companions := make([]imagefont.PreserveOptions, 0, len(source.Companions))
	for _, face := range source.Companions {
		companion, err := preservedGeometry(size, int(before.FontSize), face)
		if err != nil {
			return imagegallery.TypographyFont{}, err
		}
		companions = append(companions, companion)
	}
	display, err := gallery.FontForTypography(ctx, revision, geometry, companions...)
	if err != nil {
		return imagegallery.TypographyFont{}, err
	}
	if err := gallery.MarkRegistering(ctx, revision); err != nil {
		return imagegallery.TypographyFont{}, err
	}
	for _, font := range append(display.Related, display.DisplayFont) {
		if err := registerFont(ctx, services, font.FontPath, font.Existing); err != nil {
			return imagegallery.TypographyFont{}, err
		}
	}
	defer func() {
		if err != nil {
			// Activation may succeed even if its reply is lost to cancellation.
			// Give conditional rollback an independent, bounded opportunity.
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err = errors.Join(err, services.restore(restoreCtx, state.ProfileName, tty, display.PostScript, before))
		}
	}()
	if err := services.preserve(ctx, state.ProfileName, tty, display.PostScript, before); err != nil {
		return imagegallery.TypographyFont{}, err
	}
	after, err := services.inspect(ctx, state.ProfileName, tty)
	if err != nil {
		return imagegallery.TypographyFont{}, err
	}
	current := viewport()
	final, err := preservedGeometry(current, int(after.FontSize), source)
	if err != nil || after.FontSize != before.FontSize || after.FontName != display.PostScript || after.ProfileID != before.ProfileID || after.ProfileName != before.ProfileName || final.CellWidth != geometry.CellWidth || final.CellHeight != geometry.CellHeight {
		return imagegallery.TypographyFont{}, errors.New("Terminal font or spacing changed while preparing the image")
	}
	if current.Columns > 0 && columns >= current.Columns {
		return imagegallery.TypographyFont{}, errors.New("Terminal became too narrow while preparing the image")
	}
	if err := gallery.Commit(ctx, revision); err != nil {
		return imagegallery.TypographyFont{}, err
	}
	return display, nil
}

func registerFont(ctx context.Context, services fontServices, path string, existing bool) error {
	err := services.register(ctx, path)
	var native *imagefontmac.NativeError
	if existing && errors.As(err, &native) && native.Code == 105 {
		return nil // CoreText: this immutable font is already registered.
	}
	return err
}
