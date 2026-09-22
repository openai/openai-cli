package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"

	"github.com/openai/openai-cli/internal/imagegallery"
)

func runImageInlineTest(ctx context.Context, out io.Writer, dir, tty string, size Size, services imageFontServices) error {
	gallery, err := imagegallery.Open(ctx, dir)
	if err != nil {
		return err
	}
	state := gallery.State()
	if err := gallery.Close(); err != nil {
		return err
	}
	if !state.Initialized {
		return errors.New("run openai images inline setup in this tab first")
	}
	if err := checkImageFontWidth(state, size); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".visual-check-*.png")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = png.Encode(file, imageInlineSample())
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// A strict check: no text fallback and no successful exit on failed display.
	if err := displayImageFont(ctx, out, dir, file.Name(), tty, size, services); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Text spacing: ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefghijklmnopqrstuvwxyz 0123456789\nThe sample should have smooth color and no lines between tiles.\nYour selected profile and spacing are unchanged; previews fit the measured grid.\nIf it looks clear, this tab is ready for image commands.\nTo enable another tab: openai images inline setup\nThis check made no API call. Repeating it reuses the same cached sample.")
	return err
}

// A deterministic local check for tile edges, alpha and smooth gradients.
// It contains no user image or prompt and needs no downloaded assets.
func imageInlineSample() image.Image {
	const width, height = 384, 192
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := color.NRGBA{uint8(x * 255 / (width - 1)), uint8(y * 255 / (height - 1)), 180, 255}
			if x < 96 {
				if (x/12+y/12)%2 == 0 {
					c = color.NRGBA{255, 255, 255, 255}
				} else {
					c = color.NRGBA{30, 40, 55, 255}
				}
			}
			dx, dy := x-276, y-96
			if dx*dx+dy*dy < 64*64 {
				c = color.NRGBA{255, 135, 32, 255}
			}
			if x >= 96 && x < 144 {
				c.A = uint8(64 + y*191/(height-1))
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}
