package audio

import "math"

const audioMaxWaveformHeight = 13

// Sparse bars define the shared seek steps. Their last gap is the audio endpoint.
func audioWaveformBars(width int) int {
	if width < 3 {
		return 0
	}
	return (min(width, math.MaxUint16) - 1) / 2
}

// Dense timelines use every cell between their endpoint slots. Round each seek
// boundary to its nearest cell, including the final cell of an even-width row.
func audioWaveformMarker(width, boundary int) int {
	bars := int64(audioWaveformBars(width))
	return int((int64(boundary)*int64(width-1) + bars/2) / bars)
}

// Preserve the measured height for shading between the glyph's half-cell steps.
func audioWaveformRows(peaks []float64, width, height, selectedBoundary int, dense bool) ([]string, []float64) {
	// Native terminal dimensions fit in an unsigned 16-bit coordinate.
	width = min(width, math.MaxUint16)
	bars := audioWaveformBars(width)
	if len(peaks) == 0 || bars == 0 || height < 1 {
		return nil, nil
	}
	selectedBoundary = max(0, min(bars, selectedBoundary))
	span, marker := bars*2+1, selectedBoundary*2
	columns := bars
	maximumHeight := 5
	if dense {
		span, marker = width, audioWaveformMarker(width, selectedBoundary)
		columns = width - 2
		maximumHeight = audioMaxWaveformHeight
	}
	height = min(height, maximumHeight)
	if height%2 == 0 {
		height--
	}
	amplitudes := make([]float64, columns)
	maximum := 0.0
	// Divide before multiplying so interval boundaries cannot overflow.
	boundary := func(index int) int {
		return len(peaks)/columns*index + int(int64(len(peaks)%columns)*int64(index)/int64(columns))
	}
	for column := range columns {
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
	levels := make([]int, columns)
	for column, amplitude := range amplitudes {
		if amplitude > 0 {
			// Divide directly: a reciprocal gain can overflow for quiet peaks.
			level := amplitude / maximum * float64(resolution)
			amplitudes[column] = level
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
		cells := make([]rune, span)
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
			position := column*2 + 1
			if dense {
				position = column + 1
			}
			cells[position] = cell
		}
		cells[marker] = '┃'
		rows[row] = string(cells)
	}
	return rows, amplitudes
}
