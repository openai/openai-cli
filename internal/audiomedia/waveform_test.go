package audiomedia

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWaveformPreservesFractionalAmplitudeShape(t *testing.T) {
	peaks := []float64{.08, .18, .36, .72, .95, .55, .25, .10}
	want := "   ⣤⣿   \n  ⣤⣿⣿⣿⣀ \n⠶⣿⣿⣿⣿⣿⣿⠶\n  ⠛⣿⣿⣿⠉ \n   ⠛⣿   "
	if got := Waveform(peaks, len(peaks), 5); got != want {
		t.Fatalf("fractional shape:\n%s\nwant:\n%s", got, want)
	}
	if got := Waveform(peaks, len(peaks), 1); got != "▁▂▃▆█▅▂▁" {
		t.Fatalf("compact amplitudes: %q", got)
	}
	levels := []float64{0, .125, .25, .375, .5, .625, .75, .875, 1}
	if got := Waveform(levels, len(levels), 1); got != "─▁▂▃▄▅▆▇█" {
		t.Fatalf("compact scale: %q", got)
	}
}

func TestWaveformBrailleIsSymmetric(t *testing.T) {
	peaks := []float64{0, .08, .18, .36, .72, .95, .55, .25, .10, 1}
	// Unicode Braille numbers its left dots 1, 2, 3, 7 and right dots 4, 5, 6, 8.
	left, right := [4]rune{1, 2, 4, 64}, [4]rune{8, 16, 32, 128}
	for _, height := range []int{3, 5, 7, 31} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			rows := strings.Split(Waveform(peaks, len(peaks), height), "\n")
			for column := range peaks {
				dots := make([]bool, height*4)
				for row, text := range rows {
					cell := []rune(text)[column]
					if cell == ' ' || cell == '─' {
						continue
					}
					if cell < 0x2800 || cell > 0x28ff {
						t.Fatalf("unexpected cell %q", cell)
					}
					mask := cell - 0x2800
					for dot := range 4 {
						active := mask&left[dot] != 0
						if active != (mask&right[dot] != 0) {
							t.Fatalf("unpaired dots in cell %q", cell)
						}
						dots[row*4+dot] = active
					}
				}
				for dot, active := range dots {
					if active != dots[len(dots)-1-dot] {
						t.Fatalf("asymmetric column %d: %v", column, dots)
					}
				}
			}
		})
	}
}

func TestWaveformPreservesTransientMaximumPerTimeBin(t *testing.T) {
	peaks := []float64{.08, .95, .10, .55, .18, .25, .72}
	// Three unequal bins retain .95, .55, and .72, including the last transient.
	want := "⣿ ⣤\n⣿⣿⣿\n⣿⣿⣿\n⣿⣿⣿\n⣿ ⠛"
	if got := Waveform(peaks, 3, 5); got != want {
		t.Fatalf("transient peaks:\n%s\nwant:\n%s", got, want)
	}
	if got := Waveform(peaks, 3, 1); got != "█▅▆" {
		t.Fatalf("compact transient peaks: %q", got)
	}
	if got := Waveform(peaks, 1, 1); got != "█" {
		t.Fatalf("global transient maximum: %q", got)
	}
}

func TestWaveformUsesFixedAmplitudeScale(t *testing.T) {
	for _, height := range []int{1, 5, 31} {
		alone := strings.Split(Waveform([]float64{.1, .5}, 2, height), "\n")
		withLoudNeighbor := strings.Split(Waveform([]float64{.1, .5, 1}, 3, height), "\n")
		for row := range alone {
			if alone[row] != string([]rune(withLoudNeighbor[row])[:2]) {
				t.Fatalf("loud neighbor rescaled row %d at height %d", row, height)
			}
		}
		if Waveform([]float64{-.1, -.5}, 2, height) != strings.Join(alone, "\n") {
			t.Fatal("negative samples changed absolute amplitudes")
		}
		if Waveform([]float64{2, -2}, 2, height) != Waveform([]float64{1, 1}, 2, height) {
			t.Fatal("out-of-range amplitudes did not saturate")
		}
	}
}

func TestWaveformSilenceNonfiniteAndDimensions(t *testing.T) {
	for _, height := range []int{1, 5} {
		rows := strings.Split(Waveform([]float64{0, 0, 0}, 3, height), "\n")
		for row, got := range rows {
			want := "   "
			if row == height/2 {
				want = "───"
			}
			if got != want {
				t.Fatalf("silence at row %d: %q", row, got)
			}
		}
		invalid := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
		if got := Waveform(invalid, 3, height); got != strings.Join(rows, "\n") {
			t.Fatalf("nonfinite values invented audio: %q", got)
		}
		if got := Waveform([]float64{math.NaN(), .5, math.Inf(1)}, 1, height); got != Waveform([]float64{.5}, 1, height) {
			t.Fatal("nonfinite values hid a valid peak")
		}
	}
	for _, size := range [][4]int{
		{-1, 5, 0, 0}, {5, 0, 0, 0}, {1, 1, 1, 1}, {4, 2, 4, 1},
		{9, 4, 9, 3}, {40, 5, 40, 5}, {513, 32, 512, 31}, {math.MaxInt, math.MaxInt, 512, 31},
	} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			got := Waveform([]float64{0, .5, 1}, size[0], size[1])
			if size[2] == 0 {
				if got != "" {
					t.Fatal("nonpositive dimensions invented a waveform")
				}
				return
			}
			rows := strings.Split(got, "\n")
			if !utf8.ValidString(got) || strings.Contains(got, "\x1b") || len(rows) != size[3] {
				t.Fatal("invalid text, ANSI, or incorrect height")
			}
			for _, row := range rows {
				if utf8.RuneCountInString(row) != size[2] {
					t.Fatalf("incorrect width: %q", row)
				}
			}
		})
	}
}
