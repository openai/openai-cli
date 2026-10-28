package terminalimage

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestCellSizeUsesUnambiguousWindowMetadata(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		size                  fontViewport
		wantWidth, wantHeight int
	}{
		{"wide cells", fontViewport{100, 40, 1000, 640}, 10, 16},
		{"tall cells", fontViewport{100, 40, 800, 800}, 8, 20},
		{"partial edge cells", fontViewport{100, 40, 1009, 655}, 10, 16},
		{"nonterminating ratio", fontViewport{100, 40, 700, 600}, 7, 15},
		{"missing dimensions", fontViewport{100, 40, 0, 0}, 1, 2},
		{"missing height", fontViewport{100, 40, 1000, 0}, 1, 2},
		{"missing rows", fontViewport{100, 0, 1000, 640}, 1, 2},
		{"too small", fontViewport{100, 40, 80, 20}, 1, 2},
		{"ambiguous narrow viewport", fontViewport{2, 40, 29, 640}, 1, 2},
		{"invalid", fontViewport{-1, 40, 1000, 640}, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			width, height := cellSize(tc.size)
			require.Equal(t, tc.wantWidth, width)
			require.Equal(t, tc.wantHeight, height)
		})
	}
}

func TestColorBlocksPreserveProportionsWithMeasuredCells(t *testing.T) {
	for _, tc := range []struct {
		name                                                    string
		width, height, columns, cellWidth, cellHeight, wantRows int
	}{
		{"square wide cells", 100, 100, 32, 10, 16, 20},
		{"square tall cells", 100, 100, 40, 8, 20, 16},
		{"portrait wide cells", 100, 200, 32, 10, 16, 40},
		{"portrait tall cells", 100, 200, 40, 8, 20, 32},
		{"landscape wide cells", 200, 100, 32, 10, 16, 10},
		{"landscape tall cells", 200, 100, 40, 8, 20, 8},
		{"exact boundary with nonterminating ratio", 7, 450, 1, 7, 15, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, tc.width, tc.height))
			var out bytes.Buffer
			require.NoError(t, writeColorBlocks(&out, img, tc.columns, tc.cellWidth, tc.cellHeight))
			rows := strings.Count(out.String(), "\n") + 1
			require.Equal(t, tc.wantRows, rows)
			require.Equal(t, tc.columns*rows, strings.Count(out.String(), "▀"))
			// Compare the physical rendered rectangle against source aspect.
			require.Equal(t, tc.width*rows*tc.cellHeight, tc.height*tc.columns*tc.cellWidth)
		})
	}
}

func TestColorBlocksDoNotDuplicateOddLastSample(t *testing.T) {
	for _, height := range []int{1, 3, 5} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 4, height))
			for y := 0; y < height; y++ {
				for x := 0; x < 4; x++ {
					img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
				}
			}
			var out bytes.Buffer
			require.NoError(t, writeColorBlocks(&out, img, 4, 1, 2))
			rows := strings.Split(out.String(), "\n")
			require.Len(t, rows, (height+1)/2)
			background := ansi.Convert256(color.RGBA{24, 24, 24, 255})
			want := strings.Repeat(fmt.Sprintf("\x1b[38;5;196;48;5;%dm▀", background), 4) + "\x1b[0m"
			require.Equal(t, want, rows[len(rows)-1], "unused half-cell must not extend the image")
		})
	}
}

func TestColorBlocksRoundToNearestHalfCell(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		width, height, columns, cellWidth, cellHeight int
	}{
		{"narrow square", 100, 100, 3, 10, 16},
		{"fractional portrait", 11, 17, 9, 8, 20},
		{"fractional landscape", 17, 11, 9, 10, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, tc.width, tc.height))
			for y := 0; y < tc.height; y++ {
				for x := 0; x < tc.width; x++ {
					img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
				}
			}
			var out bytes.Buffer
			require.NoError(t, writeColorBlocks(&out, img, tc.columns, tc.cellWidth, tc.cellHeight))
			rows := strings.Split(out.String(), "\n")
			samples := len(rows) * 2
			background := fmt.Sprintf("48;5;%dm", ansi.Convert256(color.RGBA{24, 24, 24, 255}))
			if strings.Contains(rows[len(rows)-1], background) {
				samples--
			}
			physicalHeight := float64(samples*tc.cellHeight) / 2
			wantHeight := float64(tc.columns*tc.cellWidth) * float64(tc.height) / float64(tc.width)
			require.LessOrEqual(t, math.Abs(physicalHeight-wantHeight), float64(tc.cellHeight)/4)
		})
	}
}
