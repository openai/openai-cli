package cmd

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/imageopen"
	"github.com/urfave/cli/v3"
)

const imagePreviewHelp = `{{$run := or (index .Root.Metadata "help-invocation") "openai"}}View an image you already saved
  {{$run}} images preview "path/to/image.png"

Replace the quoted path with your image's location. Keep the quotes for spaces.
Supports PNG, JPEG and WebP. This makes no API request and uses no credits.

Prefer a separate window at full resolution?
  {{$run}} images preview --open "path/to/image.png"

Inline appearance depends on your terminal. Apple Terminal can offer setup
for experimental sharp previews; other terminals may show a text approximation.
The original file stays unchanged. Automatic preview on/off does not affect this command.

All options: {{$run}} help --all images preview
`

const imagePreviewDetails = "Display a local PNG, JPEG, or WebP. No API call or key is needed.\nUse --open for the original image in your default desktop viewer.\nFor sharp Apple Terminal previews: @CLI@ images inline setup (experimental).\nOtherwise, terminals without image support use a lower-detail text approximation."

// This local-only command belongs to CLI presentation, not the generated API.
func init() {
	for _, resource := range Command.Commands {
		if resource.Name == "images" {
			resource.Commands = append(resource.Commands, &cli.Command{
				Name:               "preview",
				Usage:              "Preview a saved image without generating another one.",
				UsageText:          "openai images preview [--open] FILE",
				Description:        strings.ReplaceAll(imagePreviewDetails, "@CLI@", "openai"),
				CustomHelpTemplate: imagePreviewHelp,
				Flags: []cli.Flag{&cli.BoolFlag{
					Name: "open", Usage: "Open the full-resolution original in your default image viewer", HideDefault: true,
				}},
				Action: handleImagesPreview,
			})
			return
		}
	}
}

func handleImagesPreview(ctx context.Context, cmd *cli.Command) error {
	return handleImagesPreviewWithOpener(ctx, cmd, imageopen.Open)
}

func handleImagesPreviewWithOpener(ctx context.Context, cmd *cli.Command, openImage func(context.Context, string) error) error {
	invocation, _ := cmd.Root().Metadata["help-invocation"].(string)
	if invocation == "" {
		invocation = "openai"
	}
	if cmd.Args().Len() != 1 {
		return fmt.Errorf("provide one image file: %s images preview \"path/to/image.png\"; keep paths with spaces in quotes", invocation)
	}
	if !cmd.Bool("open") && !isTerminal(cmd.Root().Writer) {
		return fmt.Errorf("image previews require terminal output; run without piping or redirecting stdout")
	}
	if format := strings.ToLower(cmd.Root().String("format")); format != "" && format != "auto" {
		return fmt.Errorf("images preview displays a local image; remove --format")
	}
	if cmd.Root().String("transform") != "" || cmd.Root().Bool("raw-output") {
		return fmt.Errorf("images preview cannot be combined with --transform or --raw-output")
	}
	path, err := filepath.Abs(cmd.Args().First())
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot find image %q: %w; check the filename and folder", path, err)
		}
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("cannot read image %q: %w; choose a file you have permission to read", path, err)
		}
		return fmt.Errorf("read image %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a folder; choose a PNG, JPEG, or WebP file inside it", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("image preview requires a regular image file")
	}
	if _, err := fmt.Fprintf(cmd.Root().Writer, "Image: %q\n", path); err != nil {
		return err
	}
	if cmd.Bool("open") {
		if err := openImage(ctx, path); err != nil {
			return explainImagePreviewFormat(err, path)
		}
		_, err := fmt.Fprintln(cmd.Root().Writer, "Opening original image in your default viewer.")
		return err
	}
	protocol := imagePreviewProtocol(true, os.Getenv)
	if protocol == "" {
		if err := prepareInteractiveImageFont(ctx, cmd.Root().Writer); err != nil {
			return err
		}
	}
	err = renderImagePreview(ctx, cmd.Root().Writer, path, protocol, imagePreviewTextColor(os.Getenv), imagePreviewTrueColor(os.Getenv))
	return explainImagePreviewFormat(err, path)
}

func explainImagePreviewFormat(err error, path string) error {
	if errors.Is(err, image.ErrFormat) {
		return fmt.Errorf("cannot preview %q as a PNG, JPEG, or WebP: %w; choose an image saved in one of those formats", path, err)
	}
	return err
}
