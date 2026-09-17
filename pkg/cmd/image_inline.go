package cmd

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
	"github.com/openai/openai-cli/internal/imagepreview"
	"github.com/urfave/cli/v3"
)

// Apple Terminal's opt-in bitmap-font renderer uses the current tab's text and
// profile settings. Setup never creates, imports, or selects another profile.
func init() {
	for _, resource := range Command.Commands {
		if resource.Name != "images" {
			continue
		}
		resource.Commands = append(resource.Commands, &cli.Command{
			Name: "inline", Usage: "Manage image previews and Apple Terminal setup.",
			Description: "Remember automatic previews with on/off on any platform.\nApple Terminal setup enables the image font in your current tab, keeping your selected profile and colors (experimental). No API calls.\nmacOS may ask for Terminal automation permission.\nUse --inline on or --inline off to override your preference for one generation.",
			Commands: append([]*cli.Command{
				{Name: "setup", Usage: "Enable sharp previews in this Apple Terminal tab.",
					Description: "Keeps your text style, font size, Inspector profile, colors and background.\nAdds image glyphs to a private local copy of your installed font; source fonts are unchanged.\nShows a local sample without opening another window or making an API call.\nIf upgrading an older preview font, select your preferred font and size in Inspector first.",
					Action:      setupImageInline},
				{Name: "test", Usage: "Check this window and show a sample image without an API call.", Action: testImageInline},
				{Name: "repair", Usage: "Repair a missing preview font and enable this tab.",
					Action: setupImageInline},
				{Name: "status", Usage: "Show the local image gallery and its capacity.",
					Flags: []cli.Flag{&cli.BoolFlag{Name: "check", Usage: "Check this tab's image font, font size and window width"}, &cli.BoolFlag{Name: "details", Usage: "Show cache path and exact preview capacity"}}, Action: statusImageInline},
				{Name: "reset", Usage: "Clear cached previews after closing tabs using image fonts.",
					Description: "Removes local preview fonts and thumbnails. Old image scrollback will no longer display.\nOriginal saved image files are kept. Run setup afterward to start a new gallery.", Action: resetImageInline},
			}, imageInlinePreferenceCommands()...),
		})
	}
}

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

func checkImageInlineOutput(cmd *cli.Command) error {
	if cmd.Args().Len() != 0 {
		return errors.New("this command takes no arguments")
	}
	if f := cmd.Root().String("format"); f != "" && f != "auto" {
		return errors.New("image preview commands use readable output; remove --format")
	}
	if cmd.Root().String("transform") != "" || cmd.Root().Bool("raw-output") {
		return errors.New("image preview commands cannot use --transform or --raw-output")
	}
	return nil
}

func checkImageInlineCommand(cmd *cli.Command) error {
	if err := checkImageInlineOutput(cmd); err != nil {
		return err
	}
	if !imagefontmac.Supported() || !localAppleImageTerminal(runtime.GOOS, os.Getenv) {
		return errors.New("run this command in Apple Terminal directly on your Mac, outside SSH or a terminal multiplexer")
	}
	return nil
}

func setupImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineCommand(cmd); err != nil {
		return err
	}
	if !isTerminal(cmd.Root().Writer) {
		return errors.New("run setup directly in the Apple Terminal tab you want to use")
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	services := nativeImageFontServices()
	file := cmd.Root().Writer.(*os.File)
	tty, err := imageFontTTY(ctx, file)
	if err != nil {
		return err
	}
	if err := setupCurrentImageFont(ctx, file, dir, tty, services); err != nil {
		return err
	}
	return runImageInlineTest(ctx, file, dir, tty, imagepreview.TerminalSize(file.Fd()), services)
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

func statusImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineOutput(cmd); err != nil {
		return err
	}
	enabled, err := imageInlinePreference()
	if err != nil {
		return err
	}
	mode := "off"
	if enabled {
		mode = "on"
	}
	if _, err := fmt.Fprintf(cmd.Root().Writer, "Automatic previews: %s\n", mode); err != nil {
		return err
	}
	if cmd.Bool("check") {
		if err := checkImageInlineCommand(cmd); err != nil {
			return err
		}
	} else if runtime.GOOS != "darwin" {
		_, err := fmt.Fprintln(cmd.Root().Writer, "Change the preference with: openai images inline on | off")
		return err
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(dir, "state.json")); errors.Is(err, os.ErrNotExist) {
		if cmd.Bool("check") {
			return errors.New("run openai images inline setup before checking this tab's font")
		}
		_, err = fmt.Fprintln(cmd.Root().Writer, "Not set up. Run: openai images inline setup")
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
	if cmd.Bool("check") {
		if !isTerminal(cmd.Root().Writer) {
			return errors.New("tab font verification requires terminal output")
		}
		if usage.MissingFiles != 0 {
			return errors.New("some preview cache files are missing; run openai images inline repair, or reset if thumbnails are missing")
		}
		services := nativeImageFontServices()
		if err := registerImageFont(ctx, services, state.FontPath, true); err != nil {
			return err
		}
		tty, err := imageFontTTY(ctx, cmd.Root().Writer.(*os.File))
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
			if _, err := fmt.Fprintln(cmd.Root().Writer, "Text uses an older fixed-font preview. Select your preferred font and size, then run: openai images inline setup"); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if _, err := fmt.Fprintf(cmd.Root().Writer, "Text font: %q at %g pt.\n", textFace.PostScript, status.FontSize); err != nil {
			return err
		}
		size := imagepreview.TerminalSize(cmd.Root().Writer.(*os.File).Fd())
		if err := checkImageFontWidth(state, size); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.Root().Writer, "Image font at %.0fpt checked; window width is sufficient.\nSpacing needs a visual check: openai images inline test\n", status.FontSize); err != nil {
			return err
		}
	}
	return printImageFontStatus(cmd.Root().Writer, state, usage, dir, cmd.Bool("details"))
}

func resetImageInline(ctx context.Context, cmd *cli.Command) error {
	if err := checkImageInlineCommand(cmd); err != nil {
		return err
	}
	dir, err := imageFontDirectory()
	if err != nil {
		return err
	}
	services := nativeImageFontServices()
	return resetImageFontCache(ctx, cmd.Root().Writer, dir, services)
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

func checkImageFontWidth(state imagegallery.State, size imagepreview.Size) error {
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
	if goos != "darwin" || getenv("TERM_PROGRAM") != "Apple_Terminal" || imagePreviewCI(getenv) {
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
func tryImageFontPreview(ctx context.Context, out io.Writer, path string, size imagepreview.Size) (bool, error) {
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

func displayImageFont(ctx context.Context, out io.Writer, dir, path, tty string, size imagepreview.Size, services imageFontServices) error {
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
		size = imagepreview.TerminalSize(file.Fd())
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
			current = imagepreview.TerminalSize(file.Fd())
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
