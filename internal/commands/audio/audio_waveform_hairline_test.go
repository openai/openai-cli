package audio

import (
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioHairlineShowsElapsedStrokesAcrossFocusChanges(t *testing.T) {
	wave := []string{" │    ┃    │ ", " │╷   ┃   ╷│ ", " │││· ┃ ·│││ ", " │╵   ┃   ╵│ ", " │    ┃    │ "}
	for _, dark := range []bool{false, true} {
		for _, state := range []struct {
			focus          int
			waveform, busy bool
		}{{0, true, false}, {4, false, false}, {4, true, false}, {4, true, true}} {
			m := audioReviewModel(t)
			m.color, m.dark, m.focus, m.waveformFocused, m.busy = true, dark, state.focus, state.waveform, state.busy
			primary, secondary, accent, border := "32;40;59", "101;112;135", "49;89;188", "186;196;216"
			if dark {
				primary, secondary, accent, border = "242;244;250", "164;172;194", "138;168;255", "81;86;107"
			}
			const marker = 6
			rows := m.workspaceHairlineRows(wave, marker)
			for row, text := range rows {
				require.Equal(t, wave[row], ansi.Strip(text))
				for column, cell := range audioHairlineCells(t, text) {
					require.False(t, cell.bold, "the glyph supplies the stroke weight")
					switch cell.glyph {
					case '│', '╷', '╵':
						stroke := secondary
						if column < marker {
							stroke = accent
						}
						require.Equal(t, stroke, cell.foreground)
					case '·':
						require.Equal(t, border, cell.foreground)
					case '┃':
						require.Equal(t, primary, cell.foreground)
					case ' ':
						require.Empty(t, cell.foreground)
					}
				}
			}
			m.color = false
			require.Equal(t, wave, m.workspaceHairlineRows(wave, marker))
		}
	}
}

func TestAudioHairlineKeepsMeasuredGeometryAndSilence(t *testing.T) {
	m := audioReviewModel(t)
	m.color, m.dark = true, true
	peaks := []float64{0, .08, .5, 1, .001, math.NaN(), math.Inf(1), -1, 0, .25}
	before := append([]float64(nil), peaks...)
	for _, tc := range []struct{ width, height int }{{49, 3}, {89, 7}, {89, 9}, {120, 9}} {
		for _, boundary := range []int{0, audioWaveformBars(tc.width) / 2, audioWaveformBars(tc.width), 1} {
			wave := audioDenseWaveform(peaks, tc.width, tc.height, boundary)
			original := append([]string(nil), wave...)
			rows := m.workspaceHairlineRows(wave, audioWaveformMarker(tc.width, boundary))
			require.Equal(t, original, wave, "shading must not edit measured geometry")
			for row, text := range rows {
				require.Equal(t, wave[row], ansi.Strip(text))
				require.LessOrEqual(t, ansi.StringWidth(text), tc.width)
				for _, cell := range audioHairlineCells(t, text) {
					if cell.glyph == '·' {
						require.Equal(t, "81;86;107", cell.foreground)
					}
				}
			}
		}
	}
	for i, value := range peaks {
		require.Equal(t, math.Float64bits(before[i]), math.Float64bits(value))
	}
	// Invalid samples cannot acquire an apparent contour through styling.
	wave := audioDenseWaveform([]float64{0, math.NaN(), math.Inf(-1), -1}, 11, 9, 0)
	for _, row := range m.workspaceHairlineRows(wave, 0) {
		for _, cell := range audioHairlineCells(t, row) {
			require.Contains(t, " ┃·", string(cell.glyph))
		}
	}
}

func TestAudioHairlinePreservesOneRowAndCompactOutput(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		r := audioReviewResult("hairline-fallback", false)
		r.analysis.Peaks = []float64{1}
		r.position = 500 * time.Millisecond
		m.result, m.focus, m.waveformFocused = &r, 4, false
		m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
		// The one-row timeline retains the center of the original three-row wave.
		// Its 53-cell span has 25 played and 25 future measured columns.
		want := "  " + m.borderStyle().Render("╶") +
			m.accent().Bold(false).Render(strings.Repeat("┼", 25)) +
			m.warningStyle().Render("╂") + m.muted().Render(strings.Repeat("┼", 25)) + m.borderStyle().Render("╴")
		require.Equal(t, audioHairlineCells(t, want), audioHairlineCells(t, m.workspaceWaveformRows(2)[0]))
		m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
		view := m.View().Content
		require.Contains(t, view, m.warningStyle().Render("╂"), "the compact playhead crosses the same measured centerline")
		require.Contains(t, view, m.warningStyle().Render("┷"))
		require.NotContains(t, ansi.Strip(view), "█")
	}
}

func TestAudioHairlineHasNoWaveformStyleToggle(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 31})
	r := audioReviewResult("hairline-style", false)
	r.position = 500 * time.Millisecond
	m.result, m.focus, m.waveformFocused = &r, 4, true
	before := m.View().Content
	require.Nil(t, audioReviewKey(m, 'v'))
	require.Equal(t, before, m.View().Content)
	require.Same(t, &r, m.result)
	require.Equal(t, 500*time.Millisecond, r.position)
}

type audioHairlineCell struct {
	glyph      rune
	foreground string
	background string
	bold       bool
}

func audioHairlineCells(t *testing.T, row string) []audioHairlineCell {
	t.Helper()
	cells := audioRenderedCells(t, row)
	for _, cell := range cells {
		require.Empty(t, cell.background, "waveform strokes must preserve the terminal background")
	}
	return cells
}

// Decode emitted styles independently of the renderer's run grouping.
func audioRenderedCells(t *testing.T, row string) []audioHairlineCell {
	t.Helper()
	tokens := regexp.MustCompile(`\x1b\[([0-9;]*)m|[^\x1b]`).FindAllStringSubmatch(row, -1)
	var cells []audioHairlineCell
	foreground, background, bold := "", "", false
	for _, token := range tokens {
		if !strings.HasPrefix(token[0], "\x1b") {
			cells = append(cells, audioHairlineCell{[]rune(token[0])[0], foreground, background, bold})
			continue
		}
		parameters := strings.Split(token[1], ";")
		for i := 0; i < len(parameters); i++ {
			switch parameters[i] {
			case "", "0":
				foreground, background, bold = "", "", false
			case "1":
				bold = true
			case "22":
				bold = false
			case "38", "48":
				require.GreaterOrEqual(t, len(parameters)-i, 5)
				require.Equal(t, "2", parameters[i+1])
				color := strings.Join(parameters[i+2:i+5], ";")
				if parameters[i] == "38" {
					foreground = color
				} else {
					background = color
				}
				i += 4
			case "49":
				background = ""
			case "39":
				foreground = ""
			default:
				t.Fatalf("unexpected style parameter %q", parameters[i])
			}
		}
	}
	return cells
}
