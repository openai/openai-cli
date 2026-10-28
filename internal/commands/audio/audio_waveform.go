package audio

import "math"

// Each bar has a gap on either side. The last gap represents the audio endpoint.
func audioWaveformBars(width int) int {
	if width < 3 {
		return 0
	}
	return (min(width, 72) - 1) / 2
}

// audioWaveform draws a relative waveform for the workbench only. One gain
// applies to the recording's measured peaks; original audio remains unchanged.
// The marker occupies a gap, so selection never replaces a measured bar.
func audioWaveform(peaks []float64, width, height, selectedBoundary int) []string {
	bars := audioWaveformBars(width)
	if len(peaks) == 0 || bars == 0 || height < 1 {
		return nil
	}
	height = min(height, 5)
	if height%2 == 0 {
		height--
	}
	selectedBoundary = max(0, min(bars, selectedBoundary))
	amplitudes := make([]float64, bars)
	maximum := 0.0
	// Divide before multiplying so interval boundaries cannot overflow.
	boundary := func(index int) int {
		return len(peaks)/bars*index + len(peaks)%bars*index/bars
	}
	for column := range bars {
		start, end := boundary(column), boundary(column+1)
		end = max(start+1, end)
		for _, value := range peaks[start:end] {
			if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
				continue
			}
			amplitudes[column] = max(amplitudes[column], min(1, value))
		}
		maximum = max(maximum, amplitudes[column])
	}
	resolution := height
	if height == 1 {
		resolution = 8
	}
	levels := make([]int, bars)
	for column, amplitude := range amplitudes {
		if amplitude > 0 {
			// Divide directly: a reciprocal gain can overflow for quiet peaks.
			level := amplitude / maximum * float64(resolution)
			if height == 1 {
				levels[column] = max(1, int(math.Ceil(level)))
			} else {
				levels[column] = int(math.Round(level))
			}
		}
	}
	blocks := [...]rune{'─', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	rows := make([]string, height)
	for row := range height {
		cells := make([]rune, bars*2+1)
		for column := range cells {
			cells[column] = ' '
		}
		for column, level := range levels {
			cell := ' '
			switch {
			case height == 1:
				cell = blocks[level]
			case level == 0:
				if row == height/2 {
					cell = '·'
				}
			default:
				// Two vertical half-cells per row keep both halves symmetric.
				top := row*2 >= height-level && row*2 < height+level
				bottom := row*2+1 >= height-level && row*2+1 < height+level
				switch {
				case top && bottom:
					cell = '│'
				case top:
					cell = '╵'
				case bottom:
					cell = '╷'
				}
			}
			cells[column*2+1] = cell
		}
		cells[selectedBoundary*2] = '┃'
		rows[row] = string(cells)
	}
	return rows
}
