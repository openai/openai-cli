package terminalimage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
)

// A tab gets its own immutable glyph map. Existing tabs that still use the old
// shared gallery retain it so upgrading cannot replace their visible scrollback.
func imageFontDirectory(ctx context.Context, out io.Writer) (string, error) {
	root, err := imageFontRoot()
	if err != nil {
		return "", err
	}
	if !isTerminal(out) {
		return root, nil
	}
	tty, err := imageFontTTY(ctx, out.(*os.File))
	if err != nil {
		return "", err
	}
	return resolveImageFontDirectory(ctx, root, os.Getenv("TERM_SESSION_ID"), tty, imagefontmac.CheckProfile, imagefontmac.Snapshot)
}

func sessionImageFontDirectory(root, sessionID, tty string) string {
	session := sha256.Sum256([]byte(sessionID + "\x00" + tty))
	return filepath.Join(root, fmt.Sprintf("%x", session[:16]))
}

func resolveImageFontDirectory(ctx context.Context, root, sessionID, tty string, check func(context.Context, string, string) error, snapshot ...func(context.Context, string, string) (imagefontmac.ProfileStatus, error)) (string, error) {
	directory := sessionImageFontDirectory(root, sessionID, tty)
	// Read legacy state only when it exists. A fresh install has no native calls
	// before the explicit setup offer and no generated profile to select.
	if _, err := os.Lstat(filepath.Join(root, "state.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return directory, nil
		}
		return "", err
	}
	if len(snapshot) != 0 {
		// A normal font cannot be using legacy image scrollback. Do not let an
		// unrelated old cache prevent a fresh tab from enabling previews.
		selected, err := snapshot[0](ctx, "OpenAI Images 00000000", tty)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(selected.FontName, "OpenAIImages-") {
			return directory, nil
		}
	}
	// Either candidate may contain stale or busy metadata. Retain those errors
	// while looking for the selected healthy gallery; do not let an unrelated
	// candidate block the font that still owns this tab's image scrollback.
	currentSelected, currentErr := imageFontGallerySelected(ctx, directory, tty, check)
	if currentSelected {
		return directory, nil
	}
	legacySelected, legacyErr := imageFontGallerySelected(ctx, root, tty, check)
	if legacySelected {
		return root, nil
	}
	if err := errors.Join(currentErr, legacyErr); err != nil {
		return "", err
	}
	return directory, nil
}

func imageFontGallerySelected(ctx context.Context, directory, tty string, check func(context.Context, string, string) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := os.Lstat(filepath.Join(directory, "state.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	gallery, err := imagegallery.OpenForReset(ctx, directory)
	if err != nil {
		return false, err
	}
	state := gallery.State()
	if err := gallery.Close(); err != nil {
		return false, err
	}
	if !state.Initialized {
		return false, nil
	}
	if err := check(ctx, state.ProfileName, tty); err != nil {
		if errors.Is(err, imagefontmac.ErrOtherProfile) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func isSessionFontDirectory(directory string) bool {
	name := filepath.Base(directory)
	return len(name) == 32 && strings.Trim(name, "0123456789abcdef") == ""
}

func bindImageFontSession(ctx context.Context, gallery *imagegallery.Gallery, dir, tty string) error {
	if !isSessionFontDirectory(dir) {
		return nil
	}
	return gallery.BindTTY(ctx, tty)
}

func cleanupSessionFontGalleries(ctx context.Context, directory, tty string) {
	if isSessionFontDirectory(directory) {
		// Failure to enumerate old tabs must never block the current preview.
		_ = cleanupClosedFontGalleries(ctx, directory, tty)
	}
}

// Cache management includes the legacy gallery and recognized tab galleries.
// Unrelated directories, symlinks and empty lock tombstones are left alone.
func imageFontCacheDirectories(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var directories []string
	if info, err := os.Lstat(filepath.Join(root, "state.json")); err == nil && info.Mode().IsRegular() {
		directories = append(directories, root)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		directory := filepath.Join(root, entry.Name())
		if !entry.IsDir() || !isSessionFontDirectory(directory) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(directory, "state.json")); err == nil {
			directories = append(directories, directory)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return directories, nil
}

func resetImageFontCaches(ctx context.Context, out io.Writer, root string, services imageFontServices) error {
	directories, err := imageFontCacheDirectories(root)
	if err != nil {
		return err
	}
	// Check every gallery before clearing any, so an active tab does not cause a
	// partially completed reset. Each reset rechecks while holding its own lock.
	for _, directory := range directories {
		gallery, err := imagegallery.OpenForReset(ctx, directory)
		if err != nil {
			return err
		}
		state := gallery.State()
		if state.Initialized {
			err = services.unused(ctx, state.ProfileName)
		}
		closeErr := gallery.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, directory := range directories {
		if err := resetImageFontCache(ctx, io.Discard, directory, services); err != nil {
			return err
		}
	}
	if len(directories) == 0 {
		_, err = fmt.Fprintln(out, "No cached previews to reset. Original images and your on/off preference are unchanged.")
		return err
	}
	return printImageFontReset(out)
}

func printImageFontReset(out io.Writer) error {
	_, err := fmt.Fprintln(out, "Cleared cached previews. Original images and your on/off preference are unchanged.\nOld image scrollback from this gallery no longer displays.\nRun openai images inline setup to enable previews again.")
	return err
}

func printImageFontCacheStatus(ctx context.Context, out io.Writer, root string, details bool) error {
	directories, err := imageFontCacheDirectories(root)
	if err != nil {
		return err
	}
	if len(directories) == 0 {
		_, err = fmt.Fprintln(out, "Not set up. Run: openai images inline setup")
		return err
	}
	if _, err := fmt.Fprintf(out, "Cached image galleries: %d\n", len(directories)); err != nil {
		return err
	}
	for _, directory := range directories {
		gallery, err := imagegallery.OpenForReset(ctx, directory)
		if err != nil {
			return err
		}
		state := gallery.State()
		usage, err := gallery.Usage()
		closeErr := gallery.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := printImageFontStatus(out, state, usage, directory, details); err != nil {
			return err
		}
	}
	return nil
}
