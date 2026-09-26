package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/urfave/cli/v3"
)

const imagePreviewHelp = `View an image you already saved
  openai images preview "path/to/image.png"

PNG, JPEG and WebP. No API call or key is needed. The original is unchanged.
Preview size uses the current terminal window. Resize, then run it again.
Automatic preview preferences do not affect this command.

  --inline auto    Use native graphics or a color approximation (default)
  --inline on      Also allow image-font activation in local Apple Terminal

Pipes, CI and terminals without supported graphics or color cannot display previews.
Preview limits: 64 MiB and 16 megapixels. Larger originals are still kept.
Full help: openai help --all images preview
`

func registerImagePreviewCommands(root *cli.Command) {
	images := root.Command("images")
	if images == nil {
		return
	}
	images.Commands = append(images.Commands,
		&cli.Command{Name: "preview", Usage: "Display a saved image without generating another one.", UsageText: "openai images preview [--inline auto|on] FILE", Description: imagePreviewHelp, CustomHelpTemplate: imagePreviewHelp,
			Flags: []cli.Flag{&cli.StringFlag{Name: "inline", Value: "auto", Usage: "Preview using auto or on; on permits a local Apple Terminal image font"}}, Action: handleImagesPreview},
	)
	inline := images.Command("inline")
	if inline == nil {
		inline = &cli.Command{Name: "inline", Usage: "Turn automatic image previews on or off."}
		images.Commands = append(images.Commands, inline)
	}
	for _, command := range imageInlinePreferenceCommands() {
		if inline.Command(command.Name) == nil {
			inline.Commands = append(inline.Commands, command)
		}
	}
}

func handleImagesPreview(ctx context.Context, command *cli.Command) error {
	if command.Args().Len() != 1 {
		return imageSavingFailure("Provide one saved image: openai images preview \"path/to/image.png\". Quote filenames containing spaces.", nil)
	}
	if err := validateLocalImageOutput(command); err != nil {
		return err
	}
	mode := command.String("inline")
	if mode != "auto" && mode != "on" {
		return imageSavingFailure("Use --inline auto or on with images preview.", nil)
	}
	out := imageCommandWriter(command)
	if !isTerminal(out) {
		return imageSavingFailure("Image previews require a terminal. Run without piping or redirecting stdout.", nil)
	}
	protocol := savedImageProtocol(mode, true, os.Getenv)
	if protocol == "" {
		return imageSavingFailure("This terminal cannot show an image preview with the current settings. Use a terminal with native graphics or color; previews are disabled in CI.", nil)
	}
	path, err := filepath.Abs(command.Args().First())
	if err != nil {
		return imageSavingFailure("Could not resolve the image path.", err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	img, err := terminalimage.ReadSaved(ctx, path)
	if err != nil {
		return imageSavingFailure("Could not preview that file. Choose a readable PNG, JPEG or WebP, at most 64 MiB and 16 megapixels. The original is unchanged.", err)
	}
	if _, err := fmt.Fprintf(out, "Image: %q\n", path); err != nil {
		return err
	}
	// Automatic previews emit best-effort warnings. An explicit preview must
	// route a failed render through the requested error format instead.
	var diagnostic bytes.Buffer
	err = displayDecodedSavedImage(ctx, img, out, &diagnostic, protocol)
	if errors.Is(err, errImagePreviewUnavailable) {
		return imageSavingFailure(strings.TrimSpace(diagnostic.String()), err)
	}
	if err != nil {
		return imageSavingFailure("Could not display the preview. The original file is unchanged.", err)
	}
	_, err = diagnostic.WriteTo(os.Stderr)
	return err
}
