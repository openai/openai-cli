package audiomedia

import (
	"context"
	"math"
	"strings"
)

// PCM16Envelope measures interleaved PCM16 from a single stream.
// It retains the same bounded peak bins as saved-file analysis.
type PCM16Envelope struct{ samples *pcmAnalyzer }

func (e *PCM16Envelope) Add(data []byte, channels int) {
	if e.samples == nil {
		if channels < 1 || channels > math.MaxUint16/2 {
			return
		}
		format := rawPCMFormat()
		format.channels, format.align = uint16(channels), uint16(channels*2)
		e.samples = newPCMAnalyzer(context.Background(), format)
	}
	// Integer PCM has no invalid sample values. Write retains partial frames.
	e.samples.Write(data)
}

func (e *PCM16Envelope) Peaks() []float64 {
	if e.samples == nil {
		return nil
	}
	return e.samples.peaks.finish()
}

// Waveform draws uncolored amplitudes on a fixed 0..1 scale. Multiple rows use
// symmetric Braille columns; a single row uses an eight-level block sparkline.
// Dimensions are bounded independently from accepted audio size.
// Zero samples produce a baseline; absent audio produces no invented waveform.
func Waveform(peaks []float64, width, height int) string {
	if len(peaks) == 0 || width < 1 || height < 1 {
		return ""
	}
	width, height = min(width, 512), min(height, 31)
	if height%2 == 0 {
		height--
	}
	// Each Braille cell has four vertical dots. Both halves share the amplitude.
	resolution := height * 2
	if height == 1 {
		resolution = 8
	}
	levels := make([]int, width)
	for column := range width {
		start := int(int64(column) * int64(len(peaks)) / int64(width))
		end := int(int64(column+1) * int64(len(peaks)) / int64(width))
		end = max(start+1, end)
		peak := 0.0
		for _, value := range peaks[start:end] {
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				peak = math.Max(peak, math.Min(1, math.Abs(value)))
			}
		}
		if peak > 0 {
			levels[column] = max(1, int(math.Ceil(peak*float64(resolution))))
		}
	}
	var output strings.Builder
	if height == 1 {
		blocks := [...]rune{'─', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
		for _, level := range levels {
			output.WriteRune(blocks[level])
		}
		return output.String()
	}
	// Paired left/right dots keep each cell bound to its maximum time-bin peak.
	dotRows := [...]rune{0x09, 0x12, 0x24, 0xc0}
	for row := range height {
		if row > 0 {
			output.WriteByte('\n')
		}
		for _, level := range levels {
			var dots rune
			for dot, mask := range dotRows {
				position := row*4 + dot
				if position >= resolution-level && position < resolution+level {
					dots |= mask
				}
			}
			switch {
			case dots != 0:
				output.WriteRune(0x2800 + dots)
			case row == height/2:
				output.WriteRune('─')
			default:
				output.WriteByte(' ')
			}
		}
	}
	return output.String()
}
