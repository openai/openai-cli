package terminalimage

import (
	"context"
	"fmt"
	"image"
	"io"
	"math"
	"math/bits"
	"strings"

	"golang.org/x/image/draw"
)

// RenderText displays a lower-detail approximation using ordinary terminal
// cells. Color fits classic block glyphs to 8×8 samples per cell, using either
// ANSI256 or explicit truecolor support. Otherwise output contains only ASCII.
// The original saved image is unchanged.
func RenderText(ctx context.Context, w io.Writer, path string, size Size, color bool, trueColor ...bool) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := loadPreviewImage(ctx, path)
	if err != nil {
		return err
	}
	columns, rows := textSize(size, source.Bounds().Dx(), source.Bounds().Dy())
	samples := 1
	if color {
		samples = 8
	}
	rgb := len(trueColor) > 0 && trueColor[0]
	small := image.NewNRGBA(image.Rect(0, 0, columns*samples, rows*samples))
	// Filter directly from the original image. Approximate bilinear sampling
	// skips most source pixels at this scale, producing jagged edges and noise.
	draw.CatmullRom.Scale(small, small.Bounds(), source, source.Bounds(), draw.Src, nil)
	if err := ctx.Err(); err != nil {
		return err
	}
	started := false
	defer func() {
		if err != nil && started && color {
			// Restore the shell's colors even when the output was interrupted.
			_, _ = io.WriteString(w, "\x1b[0m\r\n")
		}
	}()
	output := contextWriter{ctx: ctx, writer: w}
	for y := 0; y < rows; y++ {
		var line strings.Builder
		line.WriteByte('\r')
		foreground, background := textRGB{-1, -1, -1}, textRGB{-1, -1, -1}
		for x := 0; x < columns; x++ {
			if !color {
				const shades = "@%#*+=-:. "
				r, g, b := textPixel(small, x, y)
				brightness := (299*r + 587*g + 114*b + 500) / 1000
				line.WriteByte(shades[brightness*(len(shades)-1)/255])
				continue
			}
			var pixels [64]textRGB
			for i := range pixels {
				r, g, b := textPixel(small, x*8+i%8, y*8+i/8)
				pixels[i] = textRGB{r, g, b}
			}
			glyph, fg, bg := fitTextCell(pixels, rgb)
			if fg != foreground {
				writeTextColor(&line, 38, fg, rgb)
				foreground = fg
			}
			if bg != background {
				writeTextColor(&line, 48, bg, rgb)
				background = bg
			}
			line.WriteRune(glyph)
		}
		if color {
			line.WriteString("\x1b[0m")
		}
		line.WriteString("\r\n")
		started = true
		if _, err = io.WriteString(output, line.String()); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func writeTextColor(out io.Writer, layer int, c textRGB, trueColor bool) {
	if trueColor {
		fmt.Fprintf(out, "\x1b[%d;2;%d;%d;%dm", layer, c[0], c[1], c[2])
	} else {
		fmt.Fprintf(out, "\x1b[%d;5;%dm", layer, paletteColor(c[0], c[1], c[2]))
	}
}

func textSize(size Size, width, height int) (int, int) {
	columns, rows := size.Columns, size.Rows
	if columns <= 0 {
		columns = 80
	}
	if rows <= 0 {
		rows = 24
	}
	columns, rows = max(1, min(columns-2, 120)), max(1, min(rows-7, 44))
	cellAspect := 0.5
	if size.Columns > 0 && size.Rows > 0 && size.PixelWidth > 0 && size.PixelHeight > 0 {
		cellAspect = (float64(size.PixelWidth) / float64(size.Columns)) /
			(float64(size.PixelHeight) / float64(size.Rows))
	}
	aspect := float64(width) / float64(height)
	columns = min(columns, max(1, int(math.Round(float64(rows)*aspect/cellAspect))))
	rows = min(rows, max(1, int(math.Round(float64(columns)*cellAspect/aspect))))
	return columns, rows
}

// Composite premultiplied channels onto a neutral checkerboard so transparency
// remains distinguishable without guessing the user's terminal background.
func textPixel(img image.Image, x, y int) (int, int, int) {
	r, g, b, a := img.At(x, y).RGBA()
	background := 238
	if (x/4+y/4)%2 != 0 {
		background = 188
	}
	blend := func(channel uint32) int {
		return min(255, (int(channel)+background*int(65535-a)/255+128)/257)
	}
	return blend(r), blend(g), blend(b)
}

// Choose the nearer standard color-cube or grayscale entry. Avoid the first
// sixteen slots because users can customize those colors in their theme.
func paletteColor(r, g, b int) int {
	levels := [...]int{0, 95, 135, 175, 215, 255}
	nearest := func(v int) int {
		index := 0
		for i := 1; i < len(levels); i++ {
			if abs(v-levels[i]) < abs(v-levels[index]) {
				index = i
			}
		}
		return index
	}
	ri, gi, bi := nearest(r), nearest(g), nearest(b)
	gray := max(0, min(23, int(math.Round((float64(30*r+59*g+11*b)/100-8)/10))))
	v := 8 + gray*10
	source := textRGB{r, g, b}
	if colorError(source, textRGB{v, v, v}) < colorError(source, textRGB{levels[ri], levels[gi], levels[bi]}) {
		return 232 + gray
	}
	return 16 + 36*ri + 6*gi + bi
}

type textRGB [3]int

func colorError(a, b textRGB) int {
	r, g, blue := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return 30*r*r + 59*g*g + 11*blue*blue
}

func paletteRGB(index int) textRGB {
	if index >= 232 {
		v := 8 + (index-232)*10
		return textRGB{v, v, v}
	}
	levels := [...]int{0, 95, 135, 175, 215, 255}
	i := index - 16
	return textRGB{levels[i/36], levels[i/6%6], levels[i%6]}
}

type textShape struct {
	glyph rune
	mask  uint64
}

// Classic blocks are widely supported; complementary shapes use swapped
// colors. Finer cell sampling and fill symbols are established text-rendering
// techniques: https://hpjansson.org/chafa/ (this fitter is implemented here).
var textShapes = func() []textShape {
	shapes := []textShape{{' ', 0}}
	for quadrant, glyph := range []rune{' ', '▘', '▝', '▀', '▖', '▌', '▞', '▛'} {
		if quadrant == 0 {
			continue
		}
		var mask uint64
		for i := range 64 {
			if quadrant&(1<<(i%8/4+i/8/4*2)) != 0 {
				mask |= 1 << i
			}
		}
		shapes = append(shapes, textShape{glyph, mask})
	}
	for eighth, glyph := range []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇'} {
		shapes = append(shapes, textShape{glyph, ^uint64(0) << (8 * (7 - eighth))})
	}
	for eighth, glyph := range []rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉'} {
		var mask uint64
		for row := range 8 {
			mask |= ((1 << (eighth + 1)) - 1) << (row * 8)
		}
		shapes = append(shapes, textShape{glyph, mask})
	}
	return shapes
}()

type textStats struct {
	sum    textRGB
	square int
	n      int
}

func (s *textStats) add(c textRGB) {
	for i := range c {
		s.sum[i] += c[i]
	}
	s.square += colorError(c, textRGB{})
	s.n++
}

func (s textStats) mean() textRGB {
	return textRGB{(s.sum[0] + s.n/2) / s.n, (s.sum[1] + s.n/2) / s.n, (s.sum[2] + s.n/2) / s.n}
}

func (s textStats) error(c textRGB) int {
	return s.square + s.n*colorError(c, textRGB{}) - 2*(30*c[0]*s.sum[0]+59*c[1]*s.sum[1]+11*c[2]*s.sum[2])
}

func fitTextCell(pixels [64]textRGB, trueColor bool) (rune, textRGB, textRGB) {
	var total textStats
	low, high := pixels[0], pixels[0]
	for _, pixel := range pixels {
		total.add(pixel)
		for i := range pixel {
			low[i], high[i] = min(low[i], pixel[i]), max(high[i], pixel[i])
		}
	}
	quantize := func(c textRGB) textRGB {
		if !trueColor {
			return paletteRGB(paletteColor(c[0], c[1], c[2]))
		}
		return c
	}
	fg, bg := quantize(total.mean()), quantize(total.mean())
	bestError, glyph := total.error(bg), ' '
	for _, shape := range textShapes[1:] {
		var front textStats
		for mask := shape.mask; mask != 0; mask &= mask - 1 {
			front.add(pixels[bits.TrailingZeros64(mask)])
		}
		back := textStats{n: total.n - front.n, square: total.square - front.square}
		for i := range back.sum {
			back.sum[i] = total.sum[i] - front.sum[i]
		}
		a, b := quantize(front.mean()), quantize(back.mean())
		if error := front.error(a) + back.error(b); error < bestError {
			bestError, glyph, fg, bg = error, shape.glyph, a, b
		}
	}
	// Mix nearby palette colors only in smooth regions. Never replace a thin
	// boundary with a shade pattern, or add texture when exact RGB is available.
	if !trueColor && high[0]-low[0] <= 12 && high[1]-low[1] <= 12 && high[2]-low[2] <= 12 {
		neighbors := nearbyPalette(total.mean())
		for i, a := range neighbors {
			for _, b := range neighbors[i+1:] {
				if max(abs(a[0]-b[0]), abs(a[1]-b[1]), abs(a[2]-b[2])) > 95 {
					continue
				}
				for density, shade := range []rune{'░', '▒', '▓'} {
					var mixture textRGB
					for channel := range mixture {
						mixture[channel] = ((density+1)*a[channel] + (3-density)*b[channel] + 2) / 4
					}
					if error := total.error(mixture); error < bestError-bestError/10 {
						bestError, glyph, fg, bg = error, shade, a, b
					}
				}
			}
		}
	}
	return glyph, fg, bg
}

func nearbyPalette(c textRGB) []textRGB {
	levels := [...]int{0, 95, 135, 175, 215, 255}
	var bounds [3][2]int
	for channel, value := range c {
		upper := 1
		for upper < 5 && levels[upper] < value {
			upper++
		}
		bounds[channel] = [2]int{levels[upper-1], levels[upper]}
	}
	colors := make([]textRGB, 0, 10)
	for i := range 8 {
		colors = append(colors, textRGB{bounds[0][i&1], bounds[1][i>>1&1], bounds[2][i>>2&1]})
	}
	gray := max(0, min(22, (30*c[0]+59*c[1]+11*c[2]-800)/1000))
	return append(colors, paletteRGB(232+gray), paletteRGB(233+gray))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
