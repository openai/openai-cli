package terminalimage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/openai/openai-cli/internal/readable"
)

const fontSessionRequired = "sharp preview setup requires a local Apple Terminal tab on macOS, without SSH or a terminal multiplexer"

func nativeFontServices() fontServices {
	return fontServices{imagefontmac.Snapshot, imagefontmac.Source, imagefontmac.Register, imagefontmac.Preserve, imagefontmac.Restore, imagefontmac.InspectProfile}
}

// currentFontSession only identifies the caller; it never creates cache state.
func currentFontSession(ctx context.Context, out io.Writer) (*os.File, string, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", "", err
	}
	file, ok := out.(*os.File)
	if !ok || !FontSupported() {
		return nil, "", "", errors.New(fontSessionRequired)
	}
	sessionID := os.Getenv("TERM_SESSION_ID")
	if strings.TrimSpace(sessionID) == "" {
		return nil, "", "", errors.New("sharp previews require TERM_SESSION_ID; open a new Apple Terminal tab and retry")
	}
	command := exec.CommandContext(ctx, "/usr/bin/tty")
	command.Stdin = file
	command.Env = []string{}
	data, err := command.Output()
	if ctx.Err() != nil {
		return nil, "", "", ctx.Err()
	}
	if err != nil {
		return nil, "", "", errors.New("cannot identify this Apple Terminal tab; run directly in Terminal without redirecting output")
	}
	tty := strings.TrimSpace(string(data))
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, "", "", err
	}
	// A TTY may be reused. Include Terminal's session identity to keep earlier
	// tabs' immutable glyph assignments separate.
	session := sha256.Sum256([]byte(sessionID + "\x00" + tty))
	directory := filepath.Join(cache, "openai", "image-terminal", fmt.Sprintf("%x", session[:16]))
	return file, directory, tty, nil
}

// SetupFont prepares the current tab's text-preserving image font. It does not
// add an image, change automatic-preview preferences, or clean up other galleries.
func SetupFont(ctx context.Context, out io.Writer) error {
	return prepareFontSession(ctx, out, false)
}

// RepairFont rebuilds or re-registers the existing gallery using this tab's
// selected text face and size. Missing metadata cannot be reconstructed safely.
func RepairFont(ctx context.Context, out io.Writer) error {
	return prepareFontSession(ctx, out, true)
}

func prepareFontSession(ctx context.Context, out io.Writer, repair bool) error {
	file, directory, tty, err := currentFontSession(ctx, out)
	if err != nil {
		return err
	}
	return setupImageFont(ctx, out, directory, tty, repair, func() fontViewport {
		return readFontViewport(file.Fd())
	}, nativeFontServices())
}

func setupImageFont(ctx context.Context, out io.Writer, directory, tty string, repair bool, viewport func() fontViewport, services fontServices) error {
	if repair {
		cache, err := imagegallery.Inspect(ctx, directory, tty, "")
		if err != nil {
			return err
		}
		if !cache.Initialized {
			return errors.New("this tab has no preview cache to repair; earlier previews cannot be reconstructed from missing metadata; use openai images inline setup in a new Terminal tab")
		}
	}
	gallery, err := openFontGallery(ctx, directory, tty)
	if err != nil {
		return fmt.Errorf("cannot prepare this tab's preview cache; retain its files and use a new Terminal tab if the cache is damaged: %w", err)
	}
	defer gallery.Close()
	before, source, err := currentFontSource(ctx, gallery, tty, services)
	if err != nil {
		return err
	}
	revision, err := gallery.Initialize(ctx)
	if err != nil {
		return err
	}
	state := gallery.State()
	if _, err = activateImageFont(ctx, gallery, revision, state.MaxColumns, tty, before, source, viewport(), viewport, services); err != nil {
		return err
	}
	return readable.WriteText(contextWriter{ctx, out}, fmt.Sprintf("Prepared this tab for sharp previews; kept %s at %g pt and %d cached images.\nUse --inline on when generating an image. Automatic preview preferences are unchanged.", source.PostScript, before.FontSize, state.ImageCount))
}
