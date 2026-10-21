package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"strings"
)

type fontRowsKey struct{}

// PrepareRows retains independently positioned preview rows for a scrolling
// viewport. Font preparation preserves the existing explicit opt-in policy.
// Native rows retain source pixels and request exactly one terminal row.
func PrepareRows(ctx context.Context, out *os.File, img image.Image, protocol string, columns int) ([]string, error) {
	if img == nil || img.Bounds().Empty() || columns < 1 || out == nil {
		return nil, errors.New("invalid image viewport")
	}
	var text bytes.Buffer
	switch protocol {
	case "font":
		ctx = context.WithValue(ctx, fontRowsKey{}, &text)
		if err := Write(ctx, out, img, protocol, columns); err != nil {
			return nil, err
		}
	case "blocks":
		cw, ch := CellSize(out.Fd())
		if err := writeColorBlocks(contextWriter{ctx, &text}, img, columns, cw, ch); err != nil {
			return nil, err
		}
	case "kitty", "iterm", "iterm-auto", "iterm-konsole":
		cw, ch := CellSize(out.Fd())
		bounds := img.Bounds()
		rows := max(1, int((int64(bounds.Dy())*int64(columns)*int64(cw)+int64(bounds.Dx())*int64(ch)-1)/(int64(bounds.Dx())*int64(ch))))
		var result []string
		for row := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			top := bounds.Min.Y + row*bounds.Dy()/rows
			bottom := bounds.Min.Y + (row+1)*bounds.Dy()/rows
			stripe := previewStripe{Image: img, bounds: image.Rect(bounds.Min.X, top, bounds.Max.X, max(top+1, bottom))}
			text.Reset()
			if err := writePreviewStripe(ctx, &text, stripe, protocol, columns); err != nil {
				return nil, err
			}
			result = append(result, text.String())
		}
		return result, nil
	default:
		return nil, errors.New("unsupported image viewport protocol")
	}
	return strings.Split(strings.TrimSuffix(text.String(), "\n"), "\n"), nil
}

type previewStripe struct {
	image.Image
	bounds image.Rectangle
}

func (s previewStripe) Bounds() image.Rectangle { return s.bounds }
