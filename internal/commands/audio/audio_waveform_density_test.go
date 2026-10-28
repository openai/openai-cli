package audio

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestAudioDenseWaveformAddsMeasuredColumnsWithinExistingSpan(t *testing.T) {
	peaks := []float64{1}
	sparse, dense := audioWaveform(peaks, 71, 3, 0), audioDenseWaveform(peaks, 71, 3, 0)
	require.Equal(t, 35, strings.Count(sparse[1], "│"))
	require.Equal(t, 69, strings.Count(dense[1], "│"))
	for row := range dense {
		require.Equal(t, utf8.RuneCountInString(sparse[row]), utf8.RuneCountInString(dense[row]))
		require.Equal(t, "┃"+strings.Repeat("│", 69)+" ", dense[row])
	}
	// The compact renderer retains its gaps and its original sample count.
	require.Equal(t, []string{"┃▂ ▄ █ "}, audioWaveform([]float64{.25, .5, 1}, 7, 1, 0))
}

func TestAudioDenseWaveformUsesIntervalMaximaAndRecordingWideRatios(t *testing.T) {
	peaks := []float64{0, .125, .25, .375, .5, .625, .75, .875, 1}
	require.Equal(t, []string{"┃─▁▂▃▄▅▆▇█ "}, audioDenseWaveform(peaks, 11, 1, 0))
	require.Equal(t, []string{"┃  ╷╷│ ", "┃·││││ ", "┃  ╵╵│ "}, audioDenseWaveform([]float64{0, .25, .5, .75, 1}, 7, 3, 0))
	// Nine measurements divide into five unequal intervals. Each transient wins.
	intervals := []float64{.125, .01, .25, .5, .1, .01, .75, 1, .1}
	require.Equal(t, []string{"┃▁▂▄▆█ "}, audioDenseWaveform(intervals, 7, 1, 0))
	require.Equal(t, []string{"┃────█ "}, audioDenseWaveform([]float64{0, 0, 0, 0, 0, 0, 0, 0, 1}, 7, 1, 0))
	quiet := make([]float64, len(peaks))
	for i, peak := range peaks {
		quiet[i] = peak / 64
	}
	for _, height := range []int{1, 3, 5, 7, 9} {
		require.Equal(t, audioDenseWaveform(peaks, 11, height, 2), audioDenseWaveform(quiet, 11, height, 2))
	}
	require.Equal(t, []string{"┃▃▆█ "}, audioDenseWaveform([]float64{math.SmallestNonzeroFloat64, 2 * math.SmallestNonzeroFloat64, 3 * math.SmallestNonzeroFloat64}, 5, 1, 0))
}

func TestAudioDenseWaveformKeepsColumnsFixedBeneathPlayhead(t *testing.T) {
	peaks := []float64{.125, .25, .5, .75, 1, .25, .5, 1, .125}
	for _, height := range []int{1, 3, 5, 7, 9} {
		original := audioDenseWaveform(peaks, 11, height, 0)
		for _, boundary := range []int{0, 2, 5, 1} {
			rows := audioDenseWaveform(peaks, 11, height, boundary)
			for row, text := range rows {
				cells, before := []rune(text), []rune(original[row])
				require.Equal(t, 1, strings.Count(text, "┃"))
				require.Equal(t, '┃', cells[boundary*2])
				for column := 1; column < len(cells)-1; column++ {
					if column != boundary*2 {
						require.Equal(t, before[column], cells[column], "the overlay must not shift other measured columns")
					}
				}
				if boundary == 5 {
					require.Equal(t, before[len(before)-2], cells[len(cells)-2], "the endpoint must preserve the final peak")
				}
			}
		}
	}
	start, end := audioDenseWaveform([]float64{1}, 7, 3, 0), audioDenseWaveform([]float64{1}, 7, 3, 3)
	require.Equal(t, "┃│││││ ", start[1])
	require.Equal(t, " │││││┃", end[1])
}

func TestAudioDenseWaveformKeepsQuietSilenceAndMeasurements(t *testing.T) {
	invalid := []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1}
	for _, height := range []int{1, 3, 5, 7, 9} {
		require.Equal(t, audioDenseWaveform([]float64{0, 0, 0, 0}, 7, height, 1), audioDenseWaveform(invalid, 7, height, 1))
	}
	quiet := audioDenseWaveform([]float64{1, .08, .001}, 5, 5, 0)
	for row, text := range quiet {
		cells := []rune(text)
		want := ' '
		if row == 2 {
			want = '·'
		}
		require.Equal(t, want, cells[2])
		require.Equal(t, want, cells[3], "quiet tail must not become full-height bars")
	}
	peaks := []float64{0, -.5, .125, 1, 2, math.NaN(), math.Inf(1)}
	before := append([]float64(nil), peaks...)
	audioDenseWaveform(peaks, 72, 9, 18)
	for i, peak := range peaks {
		require.Equal(t, math.Float64bits(before[i]), math.Float64bits(peak), "rendering changed the measurement")
	}
}

func TestAudioDenseWaveformBoundsAndEndpoints(t *testing.T) {
	for _, size := range [][4]int{
		{-1, 3, 0, 0}, {2, 3, 0, 0}, {3, 0, 0, 0},
		{3, 1, 3, 1}, {4, 2, 3, 1}, {34, 4, 33, 3},
		{72, 5, 71, 5}, {120, 6, 119, 5}, {120, 7, 119, 7},
		{120, 8, 119, 7}, {120, 9, 119, 9}, {math.MaxInt, math.MaxInt, 119, 9},
	} {
		rows := audioDenseWaveform([]float64{0, .5, 1}, size[0], size[1], math.MaxInt)
		if size[2] == 0 {
			require.Nil(t, rows)
			continue
		}
		require.Len(t, rows, size[3])
		for _, row := range rows {
			require.True(t, utf8.ValidString(row))
			require.NotContains(t, row, "\x1b")
			require.Equal(t, size[2], utf8.RuneCountInString(row))
			require.True(t, strings.HasSuffix(row, "┃"), "the clamped endpoint must occupy the final slot")
		}
	}
	require.Equal(t, audioDenseWaveform([]float64{1}, 7, 3, 0), audioDenseWaveform([]float64{1}, 7, 3, -1))
	require.Nil(t, audioDenseWaveform(nil, 72, 5, 0))
	require.Len(t, audioWaveform([]float64{1}, 120, math.MaxInt, 0), 5, "compact rendering must retain its five-row cap")
}

func TestAudioDenseWaveformTallerRowsResolveMeasuredQuietPeaks(t *testing.T) {
	peaks := []float64{1, .08, .06, .001, 0}
	for _, tc := range []struct {
		height int
		quiet  string
	}{{5, "····"}, {7, "│···"}, {9, "││··"}} {
		rows := audioDenseWaveform(peaks, 7, tc.height, 0)
		middle := []rune(rows[tc.height/2])
		require.Equal(t, tc.quiet, string(middle[2:6]), "height may resolve measured peaks, but must not invent a silent tail")
		for row, text := range rows {
			cells := []rune(text)
			require.Equal(t, '│', cells[1], "one recording-wide gain keeps the largest peak full height")
			if row != tc.height/2 {
				require.Equal(t, "    ", string(cells[2:6]), "quiet measurements must not interpolate into taller bars")
			}
		}
	}
}
