package terminalimage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	sessionID := os.Getenv("TERM_SESSION_ID")
	if strings.TrimSpace(sessionID) == "" {
		return &FontError{errors.New("sharp previews require TERM_SESSION_ID; open a new Apple Terminal tab and retry the saved image")}
	}
	file, ok := out.(*os.File)
	if !ok || !FontSupported() {
		return &FontError{errors.New("sharp image fonts require a local Apple Terminal tab")}
	}
	command := exec.CommandContext(ctx, "/usr/bin/tty")
	command.Stdin = file
	command.Env = []string{}
	data, err := command.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return &FontError{errors.New("cannot identify this Apple Terminal tab")}
	}
	tty := strings.TrimSpace(string(data))
	cache, err := os.UserCacheDir()
	if err != nil {
		return &FontError{err}
	}
	// Each tab retains its own immutable glyph assignments. Reusing the same
	// font slot would replace images already visible in that tab's scrollback.
	session := sha256.Sum256([]byte(sessionID + "\x00" + tty))
	directory := filepath.Join(cache, "openai", "image-terminal", fmt.Sprintf("%x", session[:16]))
	services := fontServices{imagefontmac.Snapshot, imagefontmac.Source, imagefontmac.Register, imagefontmac.Preserve, imagefontmac.Restore, imagefontmac.InspectProfile}
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
	gallery, err := imagegallery.Open(ctx, directory)
	if err != nil {
		return err
	}
	defer gallery.Close()
	if err := gallery.BindTTY(ctx, tty); err != nil {
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
	} else if strings.HasPrefix(before.FontName, "OpenAIImages-") || strings.HasPrefix(before.FontName, "OpenAI Local ") || strings.HasPrefix(before.FontName, "OpenAI Image Gallery ") {
		// Another gallery (or an unresolved generated family alias) may already
		// own these codepoints in scrollback. Never replace it with a fresh font.
		return errors.New("this tab's image font cannot be matched to the current session; open a new Terminal tab to keep earlier previews intact")
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
	revision, err := gallery.Prepare(ctx, img, columns)
	if err != nil {
		if errors.Is(err, imagegallery.ErrFull) {
			return errors.New("this tab's image font is full; open a new Terminal tab to continue displaying sharp images")
		}
		return err
	}
	if size.Columns > 0 && revision.Columns >= size.Columns {
		return fmt.Errorf("widen Terminal to at least %d columns to display this cached image", revision.Columns+1)
	}
	companions := make([]imagefont.PreserveOptions, 0, len(source.Companions))
	for _, face := range source.Companions {
		companion, err := preservedGeometry(size, int(before.FontSize), face)
		if err != nil {
			return err
		}
		companions = append(companions, companion)
	}
	display, err := gallery.FontForTypography(ctx, revision, geometry, companions...)
	if err != nil {
		return err
	}
	for _, font := range append(display.Related, display.DisplayFont) {
		if err := registerFont(ctx, services, font.FontPath, font.Existing); err != nil {
			return err
		}
	}
	defer func() {
		if err != nil && !writing {
			// Activation may succeed even if its reply is lost to cancellation.
			// Give conditional rollback an independent, bounded opportunity.
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err = errors.Join(err, services.restore(restoreCtx, state.ProfileName, tty, display.PostScript, before))
		}
	}()
	if err := services.preserve(ctx, state.ProfileName, tty, display.PostScript, before); err != nil {
		return err
	}
	after, err := services.inspect(ctx, state.ProfileName, tty)
	if err != nil {
		return err
	}
	current := viewport()
	final, err := preservedGeometry(current, int(after.FontSize), source)
	if err != nil || after.FontSize != before.FontSize || after.FontName != display.PostScript || after.ProfileID != before.ProfileID || after.ProfileName != before.ProfileName || final.CellWidth != geometry.CellWidth || final.CellHeight != geometry.CellHeight {
		return errors.New("Terminal font or spacing changed while preparing the image")
	}
	if current.Columns > 0 && revision.Columns >= current.Columns {
		return errors.New("Terminal became too narrow while preparing the image")
	}
	if err := gallery.Commit(ctx, revision); err != nil {
		return err
	}
	writing = true
	_, err = io.WriteString(contextWriter{ctx, out}, display.Text)
	return err
}

func registerFont(ctx context.Context, services fontServices, path string, existing bool) error {
	err := services.register(ctx, path)
	var native *imagefontmac.NativeError
	if existing && errors.As(err, &native) && native.Code == 105 {
		return nil // CoreText: this immutable font is already registered.
	}
	return err
}
