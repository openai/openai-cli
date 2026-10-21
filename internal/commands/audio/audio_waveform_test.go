package audio

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAudioWaveformPreservesFractionalRatios(t *testing.T) {
	peaks := []float64{0, .125, .25, .375, .5, .625, .75, .875, 1}
	if got := audioWaveform(peaks, 19, 1, 0); !slices.Equal(got, []string{"┃─ ▁ ▂ ▃ ▄ ▅ ▆ ▇ █ "}) {
		t.Fatalf("compact relative amplitudes: %q", got)
	}
	want := []string{"┃    ╷ │ ", "┃· │ │ │ ", "┃    ╵ │ "}
	if got := audioWaveform([]float64{0, .25, .5, 1}, 9, 3, 0); !slices.Equal(got, want) {
		t.Fatalf("thin relative amplitudes: %q; want %q", got, want)
	}
}

func TestAudioWaveformUsesOneRecordingWideGain(t *testing.T) {
	peaks := []float64{0, .125, .25, .375, .5, .625, .75, .875, 1}
	quiet := make([]float64, len(peaks))
	for index, peak := range peaks {
		quiet[index] = peak / 64
	}
	for _, height := range []int{1, 3, 5} {
		if got, want := audioWaveform(quiet, 19, height, 4), audioWaveform(peaks, 19, height, 4); !slices.Equal(got, want) {
			t.Fatalf("recording gain changed shape at height %d: %q; want %q", height, got, want)
		}
	}
	// Subnormal measurements remain finite when normalized directly.
	quiet = []float64{math.SmallestNonzeroFloat64, 2 * math.SmallestNonzeroFloat64}
	if got := audioWaveform(quiet, 5, 1, 0); !slices.Equal(got, []string{"┃▄ █ "}) {
		t.Fatalf("subnormal amplitudes lost their ratio: %q", got)
	}
}

func TestAudioWaveformPreservesIntervalTransientMaxima(t *testing.T) {
	peaks := []float64{.08, .95, .10, .55, .18, .25, .72}
	if got := audioWaveform(peaks, 7, 1, 0); !slices.Equal(got, []string{"┃█ ▅ ▇ "}) {
		t.Fatalf("unequal interval maxima changed: %q", got)
	}
	for _, height := range []int{1, 3, 5} {
		got := audioWaveform(peaks, 7, height, 2)
		want := audioWaveform([]float64{.95, .55, .72}, 7, height, 2)
		if !slices.Equal(got, want) {
			t.Fatalf("interval maxima changed at height %d: %q; want %q", height, got, want)
		}
	}
	if got := audioWaveform([]float64{0, 0, 0, 0, 0, 0, 1}, 7, 1, 0); !slices.Equal(got, []string{"┃─ ─ █ "}) {
		t.Fatalf("last transient or silent intervals changed: %q", got)
	}
}

func TestAudioWaveformSilenceAndInvalidPeaks(t *testing.T) {
	zero := []float64{0, 0, 0}
	want := []string{"  ┃    ", " ·┃· · ", "  ┃    "}
	if got := audioWaveform(zero, 7, 3, 1); !slices.Equal(got, want) {
		t.Fatalf("silence invented variation: %q; want %q", got, want)
	}
	for _, height := range []int{1, 3, 5} {
		invalid := []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1}
		got := audioWaveform(invalid, 7, height, 1)
		want := audioWaveform(zero, 7, height, 1)
		if !slices.Equal(got, want) {
			t.Fatalf("invalid peaks invented audio: %q; want %q", got, want)
		}
		got = audioWaveform([]float64{math.NaN(), .5, math.Inf(1)}, 3, height, 0)
		want = audioWaveform([]float64{.5}, 3, height, 0)
		if !slices.Equal(got, want) {
			t.Fatalf("invalid neighbors hid valid audio: %q; want %q", got, want)
		}
		got = audioWaveform([]float64{-1, .5, 2}, 7, height, 1)
		want = audioWaveform([]float64{0, .5, 1}, 7, height, 1)
		if !slices.Equal(got, want) {
			t.Fatalf("amplitudes escaped the measured range: %q; want %q", got, want)
		}
	}
}

func TestAudioWaveformMarkerDoesNotReplaceBars(t *testing.T) {
	peaks := []float64{.25, .5, 1}
	for _, selected := range []int{-1, 0, 1, 2, 3, math.MaxInt} {
		for _, height := range []int{1, 3, 5} {
			rows := audioWaveform(peaks, 7, height, selected)
			original := audioWaveform(peaks, 7, height, 0)
			for row, text := range rows {
				cells, before := []rune(text), []rune(original[row])
				marker := max(0, min(3, selected)) * 2
				if strings.Count(text, "┃") != 1 || cells[marker] != '┃' {
					t.Fatalf("missing selected boundary %d: %q", selected, rows)
				}
				for column := 1; column < len(cells); column += 2 {
					if cells[column] != before[column] {
						t.Fatalf("selection replaced measured bar %d: %q", column, rows)
					}
				}
			}
		}
	}
}

func TestAudioWaveformDoesNotChangeMeasurements(t *testing.T) {
	peaks := []float64{0, -.5, .125, 1, 2, math.NaN(), math.Inf(1)}
	before := slices.Clone(peaks)
	audioWaveform(peaks, 72, 5, 35)
	for index, peak := range peaks {
		if math.Float64bits(peak) != math.Float64bits(before[index]) {
			t.Fatalf("rendering changed peak %d", index)
		}
	}
	if got := audioWaveform(nil, 72, 5, 0); got != nil {
		t.Fatalf("absent audio invented a waveform: %q", got)
	}
}

func TestAudioWaveformBoundsDimensions(t *testing.T) {
	for _, size := range [][4]int{
		{-1, 3, 0, 0}, {0, 3, 0, 0}, {2, 3, 0, 0}, {3, 0, 0, 0},
		{3, 1, 3, 1}, {4, 2, 3, 1}, {7, 3, 7, 3}, {34, 4, 33, 3},
		{72, 5, 71, 5}, {73, 6, 71, 5}, {math.MaxInt, math.MaxInt, 71, 5},
	} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			rows := audioWaveform([]float64{0, .5, 1}, size[0], size[1], math.MaxInt)
			if size[2] == 0 {
				if rows != nil {
					t.Fatalf("invalid dimensions produced rows: %q", rows)
				}
				return
			}
			if len(rows) != size[3] {
				t.Fatalf("height %d; want %d", len(rows), size[3])
			}
			for _, row := range rows {
				if !utf8.ValidString(row) || strings.Contains(row, "\x1b") || utf8.RuneCountInString(row) != size[2] {
					t.Fatalf("invalid row dimensions or control bytes: %q", row)
				}
			}
		})
	}
	for _, test := range [][2]int{{-1, 0}, {2, 0}, {3, 1}, {4, 1}, {34, 16}, {71, 35}, {72, 35}, {math.MaxInt, 35}} {
		if got := audioWaveformBars(test[0]); got != test[1] {
			t.Fatalf("bars at width %d: %d; want %d", test[0], got, test[1])
		}
	}
}

func TestAudioWaveformQuietTailDoesNotBecomeFullCenterBars(t *testing.T) {
	rows := audioWaveform([]float64{1, .08, .001}, 7, 5, 0)
	for row, text := range rows {
		cells := []rune(text)
		for _, column := range []int{3, 5} {
			want := ' '
			if row == 2 {
				want = '·'
			}
			if cells[column] != want {
				t.Fatalf("quiet tail is exaggerated at row %d: %q", row, rows)
			}
		}
	}
}
