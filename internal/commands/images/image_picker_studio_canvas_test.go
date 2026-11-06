package images

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImageStudioCanvasFollowsSelectedAspect(t *testing.T) {
	m, _ := browserForTest(t, 133, 39)
	m.color = false
	g, ok := imageStudioLayout(m.width, m.height)
	require.True(t, ok)
	for _, test := range []struct {
		size, caption string
		ratio         float64
	}{
		{"1536x1024", "1536 × 1024", 1.5},
		{"1024x1536", "1024 × 1536", 2.0 / 3},
		{"1024x1024", "1024 × 1024", 1},
	} {
		t.Run(test.size, func(t *testing.T) {
			m.settings.size = test.size
			rows := m.studioCanvas(g, 11)
			require.Len(t, rows, 11, "the empty state must not increase the frame height")
			require.Equal(t, "Preview", rows[0])
			require.Contains(t, strings.Join(rows, "\n"), test.caption)
			first, last, width := -1, -1, 0
			for i, row := range rows {
				if strings.Contains(row, "┌") {
					first, width = i, ansi.StringWidth(strings.TrimSpace(row))
				}
				if strings.Contains(row, "└") {
					last = i
				}
			}
			require.GreaterOrEqual(t, first, 1)
			require.Greater(t, last, first)
			visualRatio := float64(width) / float64(2*(last-first+1))
			require.LessOrEqual(t, math.Abs(visualRatio-test.ratio), 0.07)
		})
	}
}

func TestImageStudioCanvasAutoDoesNotInventDimensions(t *testing.T) {
	m, _ := browserForTest(t, 133, 39)
	m.color = false
	g, _ := imageStudioLayout(m.width, m.height)
	for _, size := range []string{"auto", "", "unexpected\x1b[31m"} {
		m.settings.size = size
		view := strings.Join(m.studioCanvas(g, 11), "\n")
		require.Contains(t, view, "Automatic size")
		require.NotContains(t, view, "┌")
		require.NotContains(t, view, "1024")
		require.NotContains(t, view, "unexpected")
		require.NotContains(t, view, "\x1b")
	}
}

func TestImageStudioCanvasFitsAvailableCells(t *testing.T) {
	m, _ := browserForTest(t, 133, 39)
	for _, profile := range []colorprofile.Profile{colorprofile.ASCII, colorprofile.ANSI, colorprofile.TrueColor} {
		m.profile, m.color = profile, profile != colorprofile.ASCII
		for _, size := range []string{"auto", "1536x1024", "1024x1536", "1024x1024"} {
			m.settings.size = size
			for _, width := range []int{0, 1, 5, 11, 20, 54, 64} {
				for _, height := range []int{0, 1, 2, 3, 4, 8, 11, 17} {
					g := imageStudioGeometry{previewWidth: width, editorRows: 17}
					rows := m.studioCanvas(g, height)
					require.LessOrEqual(t, len(rows), height)
					for _, row := range rows {
						require.LessOrEqual(t, ansi.StringWidth(row), width)
					}
				}
			}
		}
	}
}
