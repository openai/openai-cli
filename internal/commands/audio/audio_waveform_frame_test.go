package audio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioWaveformTimelineConnectsPlayheadWithoutMovingBars(t *testing.T) {
	for _, width := range []int{60, 61, 101, 186} {
		for _, height := range []int{1, 3, 5, 7, 9} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := audioReviewModel(t)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 32})
				r := audioReviewResult("frame-seek", false)
				r.analysis.Peaks = []float64{0, .125, .5, 1, .25, 0}
				m.result, m.focus, m.waveformFocused = &r, 4, true
				before := append([]float64(nil), r.analysis.Peaks...)
				var originalBars []string
				start, end := 0, 0
				for step, position := range []time.Duration{0, 500 * time.Millisecond, time.Second, -1} {
					if position < 0 {
						require.Nil(t, audioReviewKey(m, tea.KeyLeft))
						require.Less(t, r.position, time.Second, "backward seek must move the playhead")
					} else {
						r.position = position
					}
					rows := m.workspaceWaveformRows(height + 1)
					require.Len(t, rows, height+1, "one lower rail follows the measured rows")
					require.Equal(t, rows, m.workspaceWaveformRows(height+2), "an extra row cannot add an even waveform height")
					marker := audioWaveformFrameColumn(t, rows[len(rows)-1], "┷")
					switch step {
					case 0:
						start = marker
						require.Equal(t, 2, start, "zero must align with the content gutter")
					case 1:
						require.Greater(t, marker, start)
					case 2:
						end = marker
						require.Greater(t, end, start)
					case 3:
						require.Equal(t, end-2, marker, "one backward seek moves one waveform gap")
					}
					// The measured renderer is the shape oracle. The timeline may crop
					// outer rows; only the playhead may cover one fixed display column.
					wave := audioDenseWaveform(before, m.waveformWidth(), max(3, height), m.selectedBoundary())
					wave = wave[(len(wave)-height)/2 : (len(wave)+height)/2]
					var bars []string
					for i, row := range rows[:height] {
						cells := []rune(ansi.Strip(row))
						markerGlyph := "┃"
						if i == height/2 {
							switch marker {
							case start:
								markerGlyph = "┠"
							case len(cells) - 1:
								markerGlyph = "┨"
							default:
								markerGlyph = "╂"
							}
							require.Empty(t, strings.Trim(string(cells[start:]), "╶╴┠┨╂─┼"), "every centerline cell must join its neighbor")
						}
						require.Equal(t, marker, audioWaveformFrameColumn(t, row, markerGlyph))
						waveCells := cells[start+1 : start+ansi.StringWidth(wave[i])-1]
						want := []rune(wave[i])
						got := strings.NewReplacer("─", "·", "┼", "│", "╂", "┃").Replace(string(waveCells))
						require.Equal(t, string(want[1:len(want)-1]), got)
						bars = append(bars, strings.ReplaceAll(got, "┃", " "))
					}
					if step == 0 {
						originalBars = bars
					} else {
						for i, row := range bars {
							original := []rune(originalBars[i])
							for column, cell := range []rune(row) {
								if column != marker-start-1 {
									require.Equal(t, original[column], cell, "seeking must not shift adjacent measured columns")
								}
							}
						}
					}
					for _, row := range rows {
						require.False(t, strings.ContainsAny(row, "┄┆╭╮╰╯"), "the waveform must remain unboxed")
						require.Equal(t, ansi.StringWidth(rows[0]), ansi.StringWidth(row))
						require.Less(t, ansi.StringWidth(row), width)
					}
				}
				require.Equal(t, before, r.analysis.Peaks)
				if width >= 132 {
					require.Equal(t, 118, end-start, "wide timelines must preserve the capped waveform span")
				}
			})
		}
	}
}

func TestAudioWaveformTimelineKeepsNeutralRailAndCompletionAnchor(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, focused := range []bool{false, true} {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: 101, Height: 33})
			m.color, m.dark = true, dark
			r := audioReviewResult("frame-color", false)
			r.analysis.Peaks = []float64{0, 1, 0, 1, 0, 1}
			m.result, m.focus, m.waveformFocused = &r, 4, focused
			border, secondary, accent, green := "186;196;216", "101;112;135", "49;89;188", "35;107;67"
			if dark {
				border, secondary, accent, green = "81;86;107", "164;172;194", "138;168;255", "141;201;160"
			}
			stroke := secondary
			if focused {
				stroke = accent
			}
			for _, state := range []struct {
				position          time.Duration
				finished, playing bool
			}{
				{0, false, false}, {500 * time.Millisecond, false, false}, {time.Second, false, false},
				{time.Second, true, false}, {time.Second, true, true}, {250 * time.Millisecond, false, false},
			} {
				r.position, r.finished, m.playing = state.position, state.finished, state.playing
				rows := m.workspaceWaveformRows(4)
				greenCells := 0
				for index, row := range rows {
					for _, cell := range audioHairlineCells(t, row) {
						if strings.ContainsRune("│╷╵┼┃╂┠┨┷", cell.glyph) {
							require.False(t, cell.bold, "the measured strokes and playhead use regular weight")
						}
						switch cell.glyph {
						case '─', '├', '┤', '╶', '╴':
							require.Equal(t, border, cell.foreground, "rail and silence remain neutral, including focus")
						case '│', '╷', '╵', '┼':
							require.Equal(t, stroke, cell.foreground)
						}
						if cell.foreground == green {
							greenCells++
							require.Equal(t, len(rows)-1, index)
							require.Equal(t, '┷', cell.glyph)
						}
					}
				}
				want := 0
				if state.finished && !state.playing {
					want = 1
				}
				require.Equal(t, want, greenCells, "a seek-to-end alone must never claim completion")
				m.color = false
				plain := m.workspaceWaveformRows(4)
				for i := range rows {
					require.Equal(t, ansi.Strip(rows[i]), plain[i])
					require.NotContains(t, plain[i], "\x1b")
				}
				m.color = true
			}
		}
	}
}

func TestAudioWaveformTimelineDoesNotInventPlayheadWithoutMeasuredAudio(t *testing.T) {
	for _, state := range []string{"empty", "unmeasured", "no peaks"} {
		m := audioReviewModel(t)
		if state != "empty" {
			r := audioReviewResult("frame-unavailable", false)
			r.measured = state != "unmeasured"
			if state == "no peaks" {
				r.analysis.Peaks = nil
			}
			m.result = &r
		}
		for _, size := range [][2]int{{60, 2}, {101, 4}, {186, 6}, {101, 8}, {186, 10}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: 32})
			require.Empty(t, m.workspaceWaveformRows(size[1]), "unknown audio must reserve no waveform rows")
			header := strings.Join(m.workspaceAudioHeader(), "\n")
			if state == "empty" {
				require.Contains(t, header, "No recording yet")
			} else {
				require.Contains(t, header, "Preview unavailable")
			}
		}
	}
}

func TestAudioWaveformTimelineKeepsEndpointPeaksAndQuietIntervals(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 31})
	r := audioReviewResult("timeline-endpoints", false)
	r.analysis.Peaks = make([]float64, 87)
	r.analysis.Peaks[0], r.analysis.Peaks[86] = 1, .5
	m.result = &r
	before := append([]float64(nil), r.analysis.Peaks...)
	for _, position := range []time.Duration{0, time.Second} {
		r.position = position
		rows := m.workspaceWaveformRows(10)
		middle, rail := []rune(rows[4]), []rune(rows[9])
		require.Len(t, middle, 91, "two gutter cells plus the unchanged 89-cell timeline")
		require.Equal(t, '┼', middle[3], "the first measured interval follows the zero slot")
		require.Equal(t, '┼', middle[89], "the final measured interval precedes the duration slot")
		require.Equal(t, strings.Repeat("─", 85), string(middle[4:89]), "quiet intervals remain flat between endpoint peaks")
		if position == 0 {
			require.Equal(t, "  ┷"+strings.Repeat("─", 87)+"┤", string(rail))
			require.Equal(t, '┠', middle[2])
			require.Equal(t, '╴', middle[90])
		} else {
			require.Equal(t, "  ├"+strings.Repeat("─", 87)+"┷", string(rail))
			require.Equal(t, '╶', middle[2])
			require.Equal(t, '┨', middle[90])
		}
		for _, row := range rows {
			require.Len(t, []rune(row), 91, "there must be no decorative extension beyond the duration slot")
		}
	}
	require.Equal(t, before, r.analysis.Peaks)
	r.analysis.Peaks = []float64{0}
	rows := m.workspaceWaveformRows(10)
	require.Equal(t, "  ╶"+strings.Repeat("─", 87)+"┨", rows[4])
	for _, row := range rows[:9] {
		require.NotContains(t, row, "│")
		require.NotContains(t, row, "╷")
		require.NotContains(t, row, "╵")
	}
	require.Equal(t, "  ├"+strings.Repeat("─", 87)+"┷", rows[9], "silent audio still has its full duration")
}

func audioWaveformFrameColumn(t *testing.T, row, glyph string) int {
	t.Helper()
	row = ansi.Strip(row)
	require.Equal(t, 1, strings.Count(row, glyph))
	return ansi.StringWidth(row[:strings.Index(row, glyph)])
}
