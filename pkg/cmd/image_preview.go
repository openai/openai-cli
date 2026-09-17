package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/imageopen"
	"github.com/urfave/cli/v3"
)

// This local-only command belongs to CLI presentation, not the generated API.
func init() {
	for _, resource := range Command.Commands {
		if resource.Name == "images" {
			resource.Commands = append(resource.Commands, &cli.Command{
				Name:        "preview",
				Usage:       "Preview a saved image without generating another one.",
				UsageText:   "openai images preview [--open] FILE",
				Description: "Display a local PNG, JPEG, or WebP. No API call or key is needed.\nUse --open for the original image in your default desktop viewer.\nFor sharp Apple Terminal previews: openai images inline setup (experimental).\nOtherwise, terminals without image support use a lower-detail text approximation.",
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
	if cmd.Args().Len() != 1 {
		return fmt.Errorf("provide one image file: openai images preview FILE")
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
		return fmt.Errorf("read image %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("image preview requires a regular image file")
	}
	if _, err := fmt.Fprintf(cmd.Root().Writer, "Image: %q\n", path); err != nil {
		return err
	}
	if cmd.Bool("open") {
		if err := openImage(ctx, path); err != nil {
			return err
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
	return renderImagePreview(ctx, cmd.Root().Writer, path, protocol, imagePreviewTextColor(os.Getenv), imagePreviewTrueColor(os.Getenv))
}
