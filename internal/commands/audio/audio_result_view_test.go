package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/stretchr/testify/require"
)

func TestAudioSaveSuggestionUsesRetainedTextAndFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := audioReviewModel(t)
	r := audioReviewResult("retained", false)
	r.entry.Settings.Input, r.entry.Settings.Format = "The train leaves at five.", "wav"
	m.result = &r
	m.input.SetValue("A newer draft")
	m.settings.Format = "mp3"
	m.openSave()
	want := filepath.Join(home, "Downloads", "gpt-audio", "train-leaves-at-five.wav")
	require.Equal(t, filepath.Base(want), m.path.Value())
	require.Equal(t, audioDefaultSaveFolder, m.saveFolder.Value())
	_, err := os.Stat(filepath.Dir(want))
	require.ErrorIs(t, err, os.ErrNotExist, "opening Save must not create a folder")
}

func TestAudioSaveViewShowsWrappedPathAndKeepsControlsVisible(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for index, path := range []string{
			"/Users/example/Downloads/gpt-audio/hello-from-the-workbench-synthetic.wav",
			strings.Repeat("a", 28) + ".wav",
			strings.Repeat("b", 60) + ".wav",
			"/Users/example/Downloads/gpt-audio/a filename with several spaces.wav",
			"/Users/example/Downloads/gpt-audio/こんにちは世界-音声.wav",
		} {
			t.Run(fmt.Sprintf("%dx%d/path_%d", size[0], size[1], index), func(t *testing.T) {
				m := audioReviewModel(t)
				r := audioReviewResult("retained", false)
				m.result, m.modal = &r, "save"
				m.path.SetValue(path)
				m.path.Focus()
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.note = "Enter a filename for saving."
				view := ansi.Strip(m.View().Content)
				visible := strings.ReplaceAll(strings.Join(strings.Fields(view), ""), "│", "")
				require.Contains(t, visible, strings.Join(strings.Fields(path), ""))
				for _, hint := range []string{"Enter save", "Tab edit", "Esc back"} {
					require.Contains(t, view, hint)
				}
				require.Contains(t, view, m.note)
				require.Equal(t, path, m.path.Value())
				require.LessOrEqual(t, len(strings.Split(view, "\n")), size[1])
				for _, row := range strings.Split(view, "\n") {
					require.LessOrEqual(t, ansi.StringWidth(row), size[0]-1)
				}
				if size[0] == 40 {
					for _, row := range m.plainEditorRows("Filename", m.path, true)[1:] {
						require.True(t, strings.HasPrefix(ansi.Strip(row), "  "))
					}
				}
			})
		}
	}
}

func TestAudioResultViewKeepsWaveformAndControlsVisible(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {80, 24}, {100, 32}} {
		for _, saved := range []int{0, 30} {
			t.Run(fmt.Sprintf("%dx%d/saved_%d", size[0], size[1], saved), func(t *testing.T) {
				m := audioReviewModel(t)
				r := audioReviewResult("synthetic-result", false)
				r.entry.Bytes = 108044
				r.analysis.Duration = 2.25
				r.analysis.Peaks = []float64{0, .001, .005, .02, .04, .035, .018, .008, 0, 0, .01, .004, 0}
				r.position = 750 * time.Millisecond
				for i := range saved {
					r.savedPaths = append(r.savedPaths, fmt.Sprintf("synthetic-speech-%02d.wav", i))
				}
				m.result, m.focus = &r, 4
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for action := range 2 {
					m.resultAction = action
					view := ansi.Strip(m.View().Content)
					require.Contains(t, view, "00:00.750 / 00:02.250")
					require.Contains(t, view, "╂", "the framed playhead crosses the centerline")
					require.Contains(t, view, "┼", "measured speech must retain a visible thin shape")
					require.NotContains(t, view, "█")
					if saved == 0 {
						require.NotContains(t, view, "Save to")
					} else {
						require.Contains(t, view, "Saved: synthetic-speech-29.wav")
						require.NotContains(t, view, "Save to  "+audioDefaultSaveFolder)
					}
					require.Contains(t, view, []string{"› Play audio", "› Save audio…"}[action])
					require.Contains(t, view, []string{"Space play", "Enter save"}[action])
					require.Contains(t, view, "Esc back")
					if action == 1 {
						require.NotContains(t, view, "←→ seek")
					}
					require.Contains(t, view, "105.5 KiB")
					require.NotContains(t, view, "108044 bytes")
					require.NotContains(t, view, "⣿")
					rows := strings.Split(view, "\n")
					if size[1] >= 24 && size[0] >= 60 {
						for index, row := range rows {
							if strings.Contains(row, "00:00.750 / 00:02.250") {
								require.Greater(t, index, 0)
								require.NotContains(t, rows[index-1], "┃", "timing must stay below the waveform timeline")
							}
						}
					}
					require.LessOrEqual(t, len(rows), size[1])
					for _, row := range rows {
						require.LessOrEqual(t, ansi.StringWidth(row), size[0]-1)
					}
					if saved == 0 && action == 0 {
						t.Logf("Actual synthetic model view:\n%s", view)
					}
				}
			})
		}
	}
}

func TestAudioResultViewLongTimesAndEndpoint(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("long-result", false)
	r.analysis.Duration = 9000000000
	r.position = 8999999999 * time.Second
	m.result, m.focus = &r, 4
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "149999999:59.000 / 150000000:00.000")
	require.Contains(t, view, "› Play audio")
	r.position = r.seekDuration()
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "At end")
	require.Contains(t, view, "Save audio…")
}

func TestAudioCompactSeekKeepsRailAfterFailedRetry(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("retained", false)
	r.savedPaths = []string{"saved.wav"}
	m.result = &r
	m.input.SetValue("Changed draft")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(audioWorkMessage{kind: "generate", err: audioUnauthorizedFailure()})
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	audioReviewKey(m, tea.KeyUp)
	view := ansi.Strip(m.View().Content)
	require.True(t, m.waveformFocused)
	for _, marker := range []string{"┷", "Saved:", "F1 details", "←→ seek"} {
		require.Contains(t, view, marker)
	}
	rows := strings.Split(view, "\n")
	for i, row := range rows {
		if strings.Contains(row, "┷") {
			require.Positive(t, i)
			require.Contains(t, rows[i-1], "┠", "the smallest player keeps its measured row beside the rail")
		}
	}
}

func TestAudioPlaybackStatusColumnEndsAtLastTimelineCell(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {137, 41}} {
		m := audioReviewModel(t)
		r := audioReviewResult("timeline-status", false)
		r.entry.Settings = m.effectiveSettings()
		m.result, m.focus = &r, 4
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		labelStart, railEnd := -1, -1
		for _, row := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			if before, _, found := strings.Cut(row, "Ready"); found {
				labelStart = ansi.StringWidth(before)
			}
			if before, _, found := strings.Cut(row, "┤"); found {
				railEnd = ansi.StringWidth(before) + 1
			}
		}
		require.Positive(t, railEnd)
		require.Positive(t, labelStart)
		require.Equal(t, railEnd, labelStart+len("Generating"), "reserve the whole status column at %dx%d", size[0], size[1])
	}
}

func TestAudioSaveViewKeepsFailureRecoveryVisible(t *testing.T) {
	for _, focus := range []int{0, 1} {
		t.Run(fmt.Sprint(focus), func(t *testing.T) {
			m := audioReviewModel(t)
			r := audioReviewResult("retained", false)
			m.result, m.focus, m.playing, m.playID = &r, 4, true, 9
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			m.openSave()
			m.focusSave(focus)
			m.Update(tea.PasteMsg{Content: strings.Repeat("a", 90)})
			m.Update(audioWorkMessage{kind: "play", playID: 9, err: audiomedia.ErrPlaybackFailed})
			require.Equal(t, "recovery", m.modal)
			audioReviewKey(m, tea.KeyEscape)
			require.Equal(t, "save", m.modal)
			require.Equal(t, focus, m.saveFocus)
			view := ansi.Strip(m.View().Content)
			for _, row := range m.failureSummaryRows() {
				require.Contains(t, view, ansi.Strip(row))
			}
			require.Contains(t, view, "\n    Save audio\n")
			require.Contains(t, view, "Esc back")
			require.LessOrEqual(t, len(strings.Split(view, "\n")), 11)
		})
	}
}

func TestAudioResultDistinguishesSuggestedFolderFromCompletedExport(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("retained", false)
	m.result, m.focus = &r, 4
	view := ansi.Strip(m.View().Content)
	require.NotContains(t, view, "Save to")
	require.NotContains(t, view, "Saved:")
	r.savedPaths = []string{"/chosen/folder/recording.wav"}
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "Saved: /chosen/folder/recording.wav")
	require.NotContains(t, view, "Save to  "+audioDefaultSaveFolder)
}

func TestAudioPlayerGroupsActionsAndReservesConfirmedReceipt(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("retained", false)
	m.result, m.focus = &r, 4
	for _, action := range []int{0, 1} {
		m.resultAction = action
		view := ansi.Strip(m.View().Content)
		rows := strings.Split(view, "\n")
		for i, row := range rows {
			if strings.Contains(row, "Play audio") {
				require.Contains(t, row, "Save audio…")
				require.Empty(t, strings.Trim(rows[i+1], " │"))
			}
		}
		require.Contains(t, view, []string{"› Play audio", "› Save audio…"}[action])
	}
	r.savedPaths = []string{"/exports/clip.wav"}
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	for i, row := range rows {
		if strings.Contains(row, "Play audio") {
			require.Contains(t, rows[i+1], "Saved: /exports/clip.wav")
		}
	}
}

func TestAudioWaveformProgressTracksForwardAndBackwardSeek(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		r := audioReviewResult("progress", false)
		r.analysis.Peaks = []float64{.25, .5, 1, .5, .25}
		r.analysis.Duration = 10
		m.result, m.focus, m.waveformFocused = &r, 4, true
		markerColumn := func() int {
			t.Helper()
			view := m.View().Content
			for _, row := range strings.Split(view, "\n") {
				if strings.Contains(row, "┷") {
					require.NotContains(t, row, m.playbackProgressStyle().Render("┷"), "position alone is not completion")
					return audioWaveformFrameColumn(t, row, "┷")
				}
			}
			t.Fatal("waveform progress rail missing")
			return -1
		}
		start := markerColumn()
		m.seekResult("right")
		first := markerColumn()
		require.Equal(t, start+2, first)
		m.seekResult("right")
		require.Equal(t, first+2, markerColumn())
		m.seekResult("left")
		require.Equal(t, first, markerColumn())
		m.seekResult("left")
		require.Equal(t, start, markerColumn())
		r.position = r.seekDuration()
		require.Greater(t, markerColumn(), first)
		r.finished = true
		require.Contains(t, m.View().Content, m.playbackProgressStyle().Render("┷"))
		require.Contains(t, m.View().Content, m.playbackProgressStyle().Render("● Completed"))
		m.color = false
		require.NotContains(t, m.View().Content, "\x1b[38;")
		require.Contains(t, m.View().Content, "┃")
	}
}
