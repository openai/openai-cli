package audiomedia

import (
	"math"
	"strings"
)

// Waveform draws symmetric, uncolored amplitude columns. Dimensions are bounded
// for optional terminal presentation, independently from accepted audio size.
// Zero samples produce a baseline; absent audio produces no invented waveform.
func Waveform(peaks []float64, width, height int) string {
	if len(peaks) == 0 || width < 1 || height < 1 {
		return ""
	}
	width, height = min(width, 512), min(height, 31)
	if height%2 == 0 {
		height--
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
			levels[column] = max(1, int(math.Ceil(peak*float64(height/2))))
		}
	}
	var output strings.Builder
	for row := range height {
		if row > 0 {
			output.WriteByte('\n')
		}
		distance := row - height/2
		if distance < 0 {
			distance = -distance
		}
		for _, level := range levels {
			switch {
			case level > 0 && distance <= level:
				output.WriteRune('█')
			case distance == 0:
				output.WriteRune('─')
			default:
				output.WriteByte(' ')
			}
		}
	}
	return output.String()
}
