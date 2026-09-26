package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/openai/openai-cli/internal/readable"
)

// FontStatus checks the current tab and committed cache without writing cache
// files, registering fonts, changing settings, or recovering pending attempts.
// Reading Terminal's exact tab may request macOS Automation permission.
func FontStatus(ctx context.Context, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !FontSupported() {
		return readable.WriteText(contextWriter{ctx, out}, "Sharp preview setup is unavailable here.\nRun in a local Apple Terminal tab on macOS, without SSH or a terminal multiplexer.\nOther terminals use their normal image preview or colored-character fallback.")
	}
	file, directory, tty, err := currentFontSession(ctx, out)
	if err != nil {
		return err
	}
	return inspectImageFont(ctx, out, directory, tty, readFontViewport(file.Fd()), nativeFontServices())
}

func inspectImageFont(ctx context.Context, out io.Writer, directory, tty string, size fontViewport, services fontServices) error {
	cache, err := imagegallery.Inspect(ctx, directory, tty, "")
	if err != nil {
		return fmt.Errorf("cannot check this tab's preview cache; retain its files and use a new Terminal tab if the cache is damaged: %w", err)
	}
	profile := cache.ProfileName
	if !cache.Initialized {
		// Snapshot only reads the exact TTY. This unused namespace permits
		// inspection before the first gallery identity has been created.
		profile = "OpenAI Images 00000000"
	}
	current, err := services.snapshot(ctx, profile, tty)
	if err != nil {
		return err
	}
	cache, err = imagegallery.Inspect(ctx, directory, tty, current.FontName)
	if err != nil {
		return err
	}
	if isImageFont(current.FontName) && cache.FontPath == "" {
		return errors.New("this tab's image font cannot be matched to its cache; retain earlier files and open a new Terminal tab")
	}
	message := fmt.Sprintf("Terminal font: %s, %g pt\n", current.FontName, current.FontSize)
	if !cache.Initialized {
		message += "No preview cache in this session. Run openai images inline setup to prepare this tab.\n"
	} else {
		message += fmt.Sprintf("Preview cache: %d images, %d glyph slots used\n", cache.ImageCount, cache.UsedGlyphs)
		if cache.FontPath == "" {
			message += "The selected font is not this tab's preview font. Run openai images inline repair to use this font and size.\n"
		} else {
			message += "This tab's cached preview font is selected. After a restart or font/size change, run openai images inline repair.\n"
		}
		if size.Columns > 0 && cache.MaxColumns >= size.Columns {
			message += fmt.Sprintf("Widen Terminal to at least %d columns for all cached previews.\n", cache.MaxColumns+1)
		}
	}
	message += "Read-only check; font registration and visual appearance are not tested.\nUse --inline on for sharp previews; automatic preview preferences are unchanged."
	return readable.WriteText(contextWriter{ctx, out}, message)
}
