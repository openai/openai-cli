package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
)

type imageFontServices struct {
	register   func(context.Context, string) error
	unregister func(context.Context, string) error
	check      func(context.Context, string, string) error
	activate   func(context.Context, string, string, string) error
	inspect    func(context.Context, string, string) (imagefontmac.ProfileStatus, error)
	unused     func(context.Context, string) error
	snapshot   func(context.Context, string, string) (imagefontmac.ProfileStatus, error)
	preserve   func(context.Context, string, string, string, imagefontmac.ProfileStatus) error
	source     func(context.Context, string, int) (imagefontmac.SourceFont, error)
}

func nativeImageFontServices() imageFontServices {
	return imageFontServices{register: imagefontmac.Register, unregister: imagefontmac.Unregister,
		check: imagefontmac.CheckProfile, activate: imagefontmac.Activate,
		inspect: imagefontmac.InspectProfile, unused: imagefontmac.EnsureProfileUnused,
		snapshot: imagefontmac.Snapshot, preserve: imagefontmac.Preserve, source: imagefontmac.Source}
}

func imageFontDirectory() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "openai", "image-terminal"), nil
}

// The caller retains the gallery lock until this tab has selected its font.
// Setup has no dependency on an imported Terminal profile.
func prepareImageFontGallery(ctx context.Context, dir string, services imageFontServices) (*imagegallery.Gallery, error) {
	gallery, err := imagegallery.OpenForRepair(ctx, dir)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			_ = gallery.Close()
		}
	}()
	revision, err := gallery.Initialize(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(revision.FontPath); errors.Is(err, os.ErrNotExist) {
		revision, err = gallery.Repair(ctx)
		if err != nil {
			return nil, err
		}
	}
	// Publish the complete local font/cache before asking macOS to register it.
	// A denied first registration must leave retryable metadata, not orphaned
	// files that look like a damaged gallery. No image mappings change here.
	if err := gallery.Commit(ctx, revision); err != nil {
		return nil, err
	}
	if err := registerImageFont(ctx, services, revision.FontPath, revision.Existing); err != nil {
		return nil, err
	}
	ready = true
	return gallery, nil
}

func resetImageFontCache(ctx context.Context, out io.Writer, dir string, services imageFontServices) error {
	gallery, err := imagegallery.OpenForReset(ctx, dir)
	if err != nil {
		return err
	}
	defer gallery.Close()
	fonts, err := gallery.Fonts()
	if err != nil {
		return err
	}
	if state := gallery.State(); state.Initialized {
		if err := services.unused(ctx, state.ProfileName); err != nil {
			return err
		}
	}
	for _, path := range fonts {
		if err := services.unregister(ctx, path); err != nil {
			return fmt.Errorf("close Terminal tabs using image fonts, then retry reset: %w", err)
		}
	}
	if err := gallery.Clear(ctx); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Cleared cached previews. Original images and your on/off preference are unchanged.\nOld image scrollback from this gallery no longer displays.\nRun openai images inline setup to enable previews again.")
	return err
}

func printImageFontStatus(out io.Writer, state imagegallery.State, usage imagegallery.Usage, dir string, details bool) error {
	remaining := max(0, imagefont.MaxGlyphs-state.UsedGlyphs) / 512
	if _, err := fmt.Fprintf(out, "Image gallery: %q\nCached images: %d\nSpace for about %d more square previews (other shapes vary).\nPreview cache: %.1f MiB\n", state.ProfileName, state.ImageCount, remaining, float64(usage.Bytes)/(1024*1024)); err != nil {
		return err
	}
	if usage.MissingFiles != 0 {
		if _, err := fmt.Fprintf(out, "Missing preview files: %d. Try: openai images inline repair\nIf thumbnails are missing, close tabs using image fonts and run: openai images inline reset\n", usage.MissingFiles); err != nil {
			return err
		}
	}
	if remaining <= 2 {
		if _, err := fmt.Fprintln(out, "Cache nearly full. Close tabs using image fonts, then run: openai images inline reset\nReset removes old image scrollback; your saved originals stay."); err != nil {
			return err
		}
	}
	if details {
		_, err := fmt.Fprintf(out, "Preview cells: %d / %d\nPrivate cache: %q\n", state.UsedGlyphs, imagefont.MaxGlyphs, dir)
		return err
	}
	return nil
}

func checkImageFontWidth(state imagegallery.State, size Size) error {
	if size.Columns <= 0 {
		return errors.New("could not measure this Terminal window; retry the check in an interactive window")
	}
	minimum := max(9, state.MaxColumns+1)
	if size.Columns > 0 && size.Columns < minimum {
		return fmt.Errorf("this window has %d columns; widen it to at least %d columns for the cached previews; printing them again does not change their saved width", size.Columns, minimum)
	}
	return nil
}

func localAppleImageTerminal(goos string, getenv func(string) string) bool {
	if goos != "darwin" || getenv("TERM_PROGRAM") != "Apple_Terminal" || InCI(getenv) {
		return false
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ"} {
		if getenv(key) != "" {
			return false
		}
	}
	t := getenv("TERM")
	return t != "dumb" && !strings.HasPrefix(t, "screen") && !strings.HasPrefix(t, "tmux")
}

// Check the opt-in native setup before a paid generation. No preview or API
// work happens here, and an unconfigured terminal retains its usual behavior.
func preflightImageFont(ctx context.Context, out io.Writer) error {
	_, err := imageFontReady(ctx, out)
	return err
}

// Report readiness separately so the interactive caller can offer setup in an
// ordinary tab. An absent cache does not trigger any native application access.
func imageFontReady(ctx context.Context, out io.Writer) (bool, error) {
	if !localAppleImageTerminal(runtime.GOOS, os.Getenv) || !isTerminal(out) {
		return false, nil
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(filepath.Join(dir, "state.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	gallery, err := imagegallery.Open(ctx, dir)
	if err != nil {
		return false, err
	}
	defer gallery.Close()
	state := gallery.State()
	if !state.Initialized {
		return false, nil
	}
	services := nativeImageFontServices()
	if err := registerImageFont(ctx, services, state.FontPath, true); err != nil {
		return false, err
	}
	tty, err := imageFontTTY(ctx, out.(*os.File))
	if err != nil {
		return false, err
	}
	err = services.check(ctx, state.ProfileName, tty)
	if errors.Is(err, imagefontmac.ErrOtherProfile) {
		return false, nil
	}
	return err == nil, err
}

// Only explicitly configured, local, interactive Apple Terminal sessions can
// reach the automation bridge. JSON, pipes, SSH and native graphics bypass it.
func tryImageFontPreview(ctx context.Context, out io.Writer, path string, size Size) (bool, error) {
	if !localAppleImageTerminal(runtime.GOOS, os.Getenv) || !isTerminal(out) {
		return false, nil
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return true, &imageFontPreviewError{err}
	}
	if _, err := os.Lstat(filepath.Join(dir, "state.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return true, &imageFontPreviewError{err}
	}
	file := out.(*os.File)
	tty, err := imageFontTTY(ctx, file)
	if err != nil {
		return true, &imageFontPreviewError{err}
	}
	err = displayImageFont(ctx, out, dir, path, tty, size, nativeImageFontServices())
	if errors.Is(err, imagefontmac.ErrOtherProfile) {
		return false, nil
	}
	if err != nil {
		return true, &imageFontPreviewError{err}
	}
	return true, ctx.Err()
}

func displayImageFont(ctx context.Context, out io.Writer, dir, path, tty string, size Size, services imageFontServices) error {
	gallery, err := imagegallery.Open(ctx, dir)
	if err != nil {
		return err
	}
	defer gallery.Close()
	state := gallery.State()
	if !state.Initialized {
		return errors.New("run openai images inline setup first")
	}
	// Register first to restore this session after logout. Re-registration is
	// accepted only for an existing immutable revision owned by this gallery.
	if err := registerImageFont(ctx, services, state.FontPath, true); err != nil {
		return err
	}
	// Apple Terminal reports logical-point extents through TIOCGWINSZ. Its
	// current profile's spacing may differ from the font's own advances. Fit
	// bitmap tiles to that measured grid without changing any profile setting.
	if file, ok := out.(*os.File); ok && isTerminal(file) {
		size = TerminalSize(file.Fd())
	}
	if services.source != nil {
		status, err := services.inspect(ctx, state.ProfileName, tty)
		if err != nil {
			return err
		}
		if err := restoreSelectedImageFont(ctx, gallery, status.FontName, services); err != nil {
			return err
		}
		source, err := services.source(ctx, status.FontName, int(status.FontSize))
		if err == nil {
			return displayPreservedImageFont(ctx, out, gallery, path, tty, size, status, source, services)
		}
		if !errors.Is(err, imagefontmac.ErrLegacyFont) {
			return err
		}
	}
	pointSize := float64(16)
	measured := size.PixelWidth != 0 || size.PixelHeight != 0
	if measured {
		status, err := services.inspect(ctx, state.ProfileName, tty)
		if err != nil {
			return err
		}
		pointSize = status.FontSize
	} else if err := services.check(ctx, state.ProfileName, tty); err != nil {
		return err
	}
	tileWidth, tileHeight, err := imageFontTileGeometry(size, pointSize)
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
		if errors.Is(err, imagegallery.ErrFull) {
			return fmt.Errorf("%w; close tabs using image fonts, run 'openai images inline reset', then 'openai images inline setup'; saved originals are kept", err)
		}
		return err
	}
	if size.Columns > 0 && revision.Columns >= size.Columns {
		return fmt.Errorf("widen the terminal to at least %d columns to view this cached image", revision.Columns+1)
	}
	display, err := gallery.FontForGeometry(ctx, revision, tileWidth, tileHeight)
	if err != nil {
		return err
	}
	if display.FontPath != state.FontPath || !display.Existing {
		if err := registerImageFont(ctx, services, display.FontPath, display.Existing); err != nil {
			return err
		}
	}
	if err := services.activate(ctx, state.ProfileName, tty, display.PostScript); err != nil {
		return err
	}
	if measured {
		// Registration and activation can take time. Check the final font and
		// geometry after those native calls, before committing or emitting any
		// private characters, so an Inspector change cannot silently use stale
		// tile dimensions measured before font preparation.
		status, err := services.inspect(ctx, state.ProfileName, tty)
		if err != nil {
			return err
		}
		current := size
		if file, ok := out.(*os.File); ok && isTerminal(file) {
			current = TerminalSize(file.Fd())
		}
		w, h, err := imageFontTileGeometry(current, status.FontSize)
		if err != nil || w != tileWidth || h != tileHeight || status.FontName != display.PostScript {
			return errors.New("Terminal font or spacing changed while preparing the preview; retry the saved image")
		}
		if revision.Columns >= current.Columns {
			return fmt.Errorf("widen the terminal to at least %d columns to view this cached image", revision.Columns+1)
		}
	}
	if err := gallery.Commit(ctx, revision); err != nil {
		return err
	}
	// Private glyphs are emitted only after the renderer has the matching font.
	_, err = io.WriteString(out, revision.Text)
	return err
}

type imageFontPreviewError struct{ cause error }

func (e *imageFontPreviewError) Error() string {
	return fmt.Sprintf("Sharp inline preview unavailable: %q. Retry the saved file with 'openai images preview FILE' or '--open'", e.cause.Error())
}

func (e *imageFontPreviewError) Unwrap() error { return e.cause }

func registerImageFont(ctx context.Context, services imageFontServices, path string, existing bool) error {
	err := services.register(ctx, path)
	var native *imagefontmac.NativeError
	if existing && errors.As(err, &native) && native.Code == 105 {
		return nil
	}
	return err
}

func imageFontTTY(ctx context.Context, file *os.File) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/tty")
	command.Stdin = file // Reads terminal identity, not input bytes.
	command.Env = imageFontEnvironment()
	data, err := command.Output()
	if err != nil {
		return "", errors.New("cannot identify the output terminal")
	}
	tty := strings.TrimSpace(string(data))
	if !strings.HasPrefix(tty, "/dev/ttys") || strings.ContainsAny(tty, "\r\n\x00") {
		return "", errors.New("output is not a local Apple Terminal session")
	}
	return tty, nil
}

func imageFontEnvironment() []string {
	var result []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "OPENAI_") {
			result = append(result, entry)
		}
	}
	return result
}
