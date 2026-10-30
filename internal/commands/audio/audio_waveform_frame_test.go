package audio

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioWaveformTimelineConnectsPlayheadWithoutMovingBars(t *testing.T) {
	for _, width := range []int{60, 61, 101, 186} {
		for _, height := range []int{1, 3, 5, 7, 9, 11, 13} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := audioReviewModel(t)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 41})
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
					spaced := m.workspaceWaveformRows(height + 2)
					require.Equal(t, rows[:height], spaced[:height], "a spare row must preserve the measured detail")
					require.Equal(t, []string{"", rows[height]}, spaced[height:], "the spare row precedes the rail")
					marker := audioRailMarkerColumn(t, rows[len(rows)-1])
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
						require.Equal(t, 1, strings.Count(string(cells), "┃"), "one uninterrupted playhead per row")
						require.Equal(t, marker, audioWaveformFrameColumn(t, row, "┃"))
						if i == height/2 {
							for _, segment := range []string{string(cells[start:marker]), string(cells[marker+1:])} {
								require.Empty(t, strings.Trim(segment, "╶╴─┼"), "the baseline continues on either side")
							}
						}
						waveCells := cells[start+1 : start+ansi.StringWidth(wave[i])-1]
						want := []rune(wave[i])
						got := strings.NewReplacer("─", "·", "┼", "│").Replace(string(waveCells))
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
				paneWidth := m.audioWidth()
				if m.studioLayout() {
					paneWidth = m.studioMonitorWidth()
				}
				require.Equal(t, paneWidth-5, end-start, "both endpoints must align with the content gutters")
			})
		}
	}
}

func TestAudioWaveformTimelineMovesPlayedColorAndReservesSuccessForCompletion(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, focused := range []bool{false, true} {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: 101, Height: 33})
			m.color, m.dark = true, dark
			r := audioReviewResult("frame-color", false)
			r.analysis.Peaks = []float64{0, 1, 0, 1, 0, 1}
			m.result, m.focus, m.waveformFocused = &r, 4, focused
			primary, border, secondary, elapsed, green := "32;40;59", "186;196;216", "101;112;135", "21;118;86", "35;107;67"
			if dark {
				primary, border, secondary, elapsed, green = "242;244;250", "81;86;107", "164;172;194", "81;200;157", "141;201;160"
			}
			amber := "136;91;20"
			if dark {
				amber = "226;188;121"
			}

			for _, state := range []struct {
				position          time.Duration
				finished, playing bool
			}{
				{0, false, false}, {500 * time.Millisecond, false, false}, {time.Second, false, false},
				{time.Second, true, false}, {time.Second, true, true}, {250 * time.Millisecond, false, false},
			} {
				r.position, r.finished, m.playing = state.position, state.finished, state.playing
				playhead := primary
				if !state.finished && !state.playing && state.position > 0 {
					playhead = amber
				}
				rows := m.workspaceWaveformRows(4)
				greenCells := 0
				marker := audioRailMarkerColumn(t, rows[len(rows)-1])
				for index, row := range rows {
					for column, cell := range audioHairlineCells(t, row) {
						if strings.ContainsRune("│╷╵┼┃╺╾╸", cell.glyph) {
							require.False(t, cell.bold, "the measured strokes and playhead use regular weight")
						}
						switch cell.glyph {
						case '─', '╶', '╴':
							require.Equal(t, border, cell.foreground, "silence and future rail remain neutral")
							if index == len(rows)-1 {
								require.Greater(t, column, marker)
							}
						case '┃':
							require.Equal(t, marker, column)
							require.Equal(t, playhead, cell.foreground)
						case '╾', '╸', '━', '╺':
							require.Equal(t, len(rows)-1, index)
							if column == marker {
								anchor := playhead
								if state.finished && !state.playing {
									anchor = green
								}
								require.Equal(t, anchor, cell.foreground)
							} else {
								require.Less(t, column, marker)
								require.Equal(t, elapsed, cell.foreground, "the heavier rail marks elapsed time")
							}
						case '│', '╷', '╵', '┼':
							stroke := secondary
							if column < marker {
								stroke = elapsed
							}
							require.Equal(t, stroke, cell.foreground)
						}
						if cell.foreground == green {
							greenCells++
							require.Equal(t, len(rows)-1, index)
							require.Equal(t, '╸', cell.glyph)
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

func TestAudioWaveformTimelineDoesNotInventPlayheadWithoutDuration(t *testing.T) {
	for _, state := range []string{"empty", "unmeasured"} {
		m := audioReviewModel(t)
		if state != "empty" {
			r := audioReviewResult("frame-unavailable", false)
			r.measured = false
			m.result = &r
		}
		for _, size := range [][2]int{{60, 2}, {101, 4}, {186, 6}, {101, 8}, {186, 10}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: 32})
			rows := m.workspaceWaveformRows(size[1])
			plain := strings.Fields(strings.Join(rows, " "))
			require.Equal(t, []string{"╶" + strings.Repeat("─", m.waveformWidth()-2) + "╴"}, plain,
				"empty audio has only a neutral rail, without a playhead or invented peaks")
			require.LessOrEqual(t, len(rows), size[1])
			header := strings.Join(m.workspaceAudioHeader(), "\n")
			if state == "empty" {
				require.Contains(t, header, "No recording yet")
			} else {
				require.Contains(t, header, "Preview unavailable")
			}
		}
	}
}

func TestAudioDurationOnlyTimelineKeepsRailPositionAndTracksMeasuredTime(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 133, Height: 39})
	width := m.waveformWidth()
	r := audioReviewResult("duration-only", false)
	r.analysis.Peaks = nil
	for _, budget := range []int{1, 2, 9, audioMaxWaveformHeight + 4} {
		m.result = nil
		idle := m.workspaceWaveformRows(budget)
		rail := slices.Index(idle, "  ╶"+strings.Repeat("─", width-2)+"╴")
		require.GreaterOrEqual(t, rail, 0)
		if budget == audioMaxWaveformHeight+4 {
			require.Equal(t, []string{"00:00", "00:00"}, strings.Fields(idle[rail+1]))
			require.Equal(t, 2+width, ansi.StringWidth(idle[rail+1]))
		}
		r.measured, m.result = false, &r
		unknown := m.workspaceWaveformRows(budget)
		require.Len(t, unknown, len(idle))
		require.Equal(t, idle[rail], unknown[rail])
		require.NotContains(t, strings.Join(unknown, " "), "00:00")

		r.measured = true
		for _, position := range []time.Duration{0, 500 * time.Millisecond, time.Second, 250 * time.Millisecond} {
			r.position = position
			rows := m.workspaceWaveformRows(budget)
			require.Len(t, rows, len(idle))
			require.LessOrEqual(t, len(rows), budget)
			require.Empty(t, strings.TrimSpace(strings.Join(rows[:rail], "")), "duration alone cannot draw waveform bars")
			require.Equal(t, 2+width, ansi.StringWidth(rows[rail]))
			require.Equal(t, 2+audioWaveformMarker(width, m.selectedBoundary()), audioRailMarkerColumn(t, rows[rail]))
			if budget == audioMaxWaveformHeight+4 {
				require.Equal(t, []string{"00:00", "00:01"}, strings.Fields(rows[rail+1]))
			}
		}
	}
}

func TestAudioWaveformRulerUsesMeasuredDurationAndOnlySpareRows(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 133, Height: 39})
	r := audioReviewResult("ruler", false)
	r.analysis.Duration = 3.75
	r.entry.Settings = m.effectiveSettings()
	m.result = &r
	width := m.waveformWidth()
	labels := audioTimeRuler(width, r.seekDuration())
	require.Contains(t, ansi.Strip(m.View().Content), labels, "roomy workbench should expose real timestamps")
	for _, seconds := range []float64{0, 1.25, 3.75} {
		r.position = time.Duration(seconds * float64(time.Second))
		rows := m.workspaceWaveformRows(audioMaxWaveformHeight + 4)
		rail, scale := ansi.Strip(rows[len(rows)-2]), ansi.Strip(rows[len(rows)-1])
		require.True(t, audioIsMeasuredRail(rail))
		require.NotContains(t, rail, "●")
		require.Empty(t, strings.Trim(rail, " ╺━╾╸─╴"), "the rail contains only horizontal strokes")
		require.True(t, strings.HasPrefix(scale, "  00:00"))
		require.Equal(t, []string{"00:00", "00:03.75"}, strings.Fields(scale), "the ruler labels only the endpoints")
		require.True(t, strings.HasSuffix(scale, "00:03.75"))
		require.Equal(t, 2+width, ansi.StringWidth(scale))
		require.Equal(t, []string{"", ""}, rows[len(rows)-4:len(rows)-2], "lower the rail and its timestamps by one row")
		tight := m.workspaceWaveformRows(audioMaxWaveformHeight + 3)
		require.Len(t, tight, len(rows)-1)
		require.Equal(t, tight[:len(tight)-2], rows[:len(rows)-3], "the ruler cannot reduce waveform height")
		require.NotContains(t, strings.Join(tight, "\n"), "00:03.75")
	}
	// Show precision for recordings shorter than the transport's millisecond display.
	short := audioTimeRuler(width, 40*time.Microsecond)
	require.True(t, strings.HasSuffix(short, "00:00.00004"))
}

func TestAudioWaveformTimelineKeepsEndpointPeaksAndQuietIntervals(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 31})
	r := audioReviewResult("timeline-endpoints", false)
	const span = 94 // 101 terminal columns, excluding the outer frame and both gutters.
	r.analysis.Peaks = make([]float64, span-2)
	r.analysis.Peaks[0], r.analysis.Peaks[len(r.analysis.Peaks)-1] = 1, .5
	m.result = &r
	before := append([]float64(nil), r.analysis.Peaks...)
	for _, position := range []time.Duration{0, time.Second} {
		r.position = position
		rows := m.workspaceWaveformRows(10)
		middle, rail := []rune(rows[4]), []rune(rows[9])
		require.Len(t, middle, 2+span, "two gutter cells precede the complete timeline")
		require.Equal(t, '┼', middle[3], "the first measured interval follows the zero slot")
		require.Equal(t, '┼', middle[span], "the final measured interval precedes the duration slot")
		require.Equal(t, strings.Repeat("─", span-4), string(middle[4:span]), "quiet intervals remain flat between endpoint peaks")
		if position == 0 {
			require.Equal(t, "  ╺"+strings.Repeat("─", span-2)+"╴", string(rail))
			require.Equal(t, '┃', middle[2])
			require.Equal(t, '╴', middle[span+1])
		} else {
			require.Equal(t, "  ╺"+strings.Repeat("━", span-2)+"╸", string(rail))
			require.Equal(t, '╶', middle[2])
			require.Equal(t, '┃', middle[span+1])
		}
		for _, row := range rows {
			require.Len(t, []rune(row), 2+span, "there must be no decorative extension beyond the duration slot")
		}
	}
	require.Equal(t, before, r.analysis.Peaks)
	r.analysis.Peaks = []float64{0}
	rows := m.workspaceWaveformRows(10)
	require.Equal(t, "  ╶"+strings.Repeat("─", span-2)+"┃", rows[4])
	for _, row := range rows[:9] {
		require.NotContains(t, row, "│")
		require.NotContains(t, row, "╷")
		require.NotContains(t, row, "╵")
	}
	require.Equal(t, "  ╺"+strings.Repeat("━", span-2)+"╸", rows[9], "silent audio still has its full duration")
}

func audioWaveformFrameColumn(t *testing.T, row, glyph string) int {
	t.Helper()
	row = ansi.Strip(row)
	require.Equal(t, 1, strings.Count(row, glyph))
	return ansi.StringWidth(row[:strings.Index(row, glyph)])
}

func audioRailMarkerColumn(t *testing.T, row string) int {
	t.Helper()
	row = ansi.Strip(row)
	for _, glyph := range []string{"╾", "╸", "╺"} {
		if strings.Contains(row, glyph) {
			return audioWaveformFrameColumn(t, row, glyph)
		}
	}
	t.Fatal("missing playback position on rail")
	return -1
}

func audioIsMeasuredRail(row string) bool {
	row = ansi.Strip(row)
	return strings.Contains(row, "╺") && strings.ContainsAny(row, "╴╸")
}
