package audio

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestAudioFormPaletteKeepsValuesNeutralAndSelectionVisible(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		m.settingsExpanded = true
		m.input.SetValue("Neutral draft.")
		m.instructions.SetValue("Neutral delivery.")
		m.focusSection(2)
		m.setting = 1
		view := m.View().Content
		require.NotContains(t, ansi.Strip(view), "Setting        Value")
		require.Contains(t, view, m.borderStyle().Render("│")+"  "+m.strong().Render("Text to speech"))
		audioRequireTextForeground(t, view, "Neutral draft.", lipgloss.NewStyle())
		for _, field := range []struct{ label, value string }{
			{"Model", m.settings.Model}, {"Format", "WAV"}, {"Speed", "1.00 x"},
		} {
			row := m.muted().Render(fmt.Sprintf("%-17s", field.label)) + " " + field.value
			require.Contains(t, view, row, "ordinary settings need muted labels and neutral values")
			require.NotContains(t, view, m.accent().Bold(false).Render(field.value))
		}
		for _, value := range []string{m.input.Value(), m.instructions.Value()} {
			require.Contains(t, ansi.Strip(view), value)
			require.NotContains(t, view, m.accent().Bold(false).Render(value))
		}
		foreground, accent := "32;40;59", "49;89;188"
		if dark {
			foreground, accent = "242;244;250", "138;168;255"
		}
		selected := false
		for _, row := range strings.Split(view, "\n") {
			if strings.Contains(ansi.Strip(row), "› Voice") {
				require.Contains(t, row, "38;2;"+foreground)
				require.NotContains(t, row, "38;2;"+accent)
				selected = true
			}
		}
		require.True(t, selected)
		m.color = false
		plain := m.View().Content
		require.Equal(t, ansi.Strip(view), ansi.Strip(plain))
		require.Contains(t, plain, "› Voice")
		require.NotContains(t, plain, "\x1b[38;")
		require.NotContains(t, plain, "\x1b[48;")
		m.color = true
		m.focusSection(0)
		view = m.View().Content
		require.NotContains(t, ansi.Strip(view), "› Text to speech")
		require.NotNil(t, m.View().Cursor)
		audioRequireTextForeground(t, view, "Neutral draft.", lipgloss.NewStyle())
		require.NotContains(t, view, m.accent().Bold(false).Render("Text to speech"))
		require.Contains(t, view, m.strong().Render("Text to speech"))
		require.NotContains(t, ansi.Strip(view), "Setting        Value")
		m.focusSection(1)
		audioRequireTextForeground(t, m.View().Content, "Instructions", m.muted())
		audioRequireTextForeground(t, m.View().Content, "Neutral delivery.", lipgloss.NewStyle())
	}
}

func TestAudioMonochromeEditorSelection(t *testing.T) {
	for _, focus := range []int{0, 1} {
		m, editor := audioNavigationEditor(t, focus, "take")
		m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
		require.Equal(t, "e", editor.SelectedText())
		underlined := lipgloss.NewStyle().Underline(true).Render("e")
		require.Contains(t, m.View().Content, "tak"+underlined)
		editor.Blur()
		require.Contains(t, m.editorView(*editor), "tak"+underlined)
	}
}

func TestAudioFormDistinguishesGenerateFromSecondaryActions(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		m.input.SetValue("Keep this synthetic draft")
		for _, focus := range []int{0, 2} {
			m.focusSection(focus)
			m.setting = 0
			view := m.View().Content
			audioRequireTextForeground(t, view, "Generate speech", m.strong())
			audioRequireTextForeground(t, view, "Ctrl+G", m.accent())
			for _, label := range []string{"History", "More actions…"} {
				audioRequireTextForeground(t, view, label, m.muted())
			}
			require.NotContains(t, ansi.Strip(view), "View command")
			m.color = false
			require.Equal(t, ansi.Strip(view), m.View().Content)
			m.color = true
		}
	}
}

func TestAudioWorkspaceKeepsConfirmedExportsSecondary(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		r := audioReviewResult("saved-label", false)
		m.result = &r
		m.color, m.dark = true, dark
		view := m.View().Content
		require.NotContains(t, ansi.Strip(view), "Save to")
		require.NotContains(t, ansi.Strip(view), "Saved:")
		r.savedPaths = []string{"/exports/confirmed.wav"}
		view = m.View().Content
		require.Contains(t, view, m.muted().Render("Saved: /exports/confirmed.wav"))
		require.Contains(t, view, "confirmed.wav")
		require.NotContains(t, view, m.playbackProgressStyle().Render("/exports/confirmed.wav"))
		m.color = false
		plain := m.View().Content
		require.Equal(t, ansi.Strip(view), ansi.Strip(plain))
		require.Contains(t, plain, "Saved: /exports/confirmed.wav")
	}
}

func TestAudioPlayerFeedbackUsesSemanticPalette(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		m.focusSection(4)
		audioReviewKey(m, tea.KeySpace)
		view := m.View().Content
		require.Contains(t, view, m.warningStyle().Render(audioNoGenerationNotice))
		require.NotContains(t, ansi.Strip(view), "Save audio…")
		require.NotContains(t, view, m.playbackProgressStyle().Render(audioNoGenerationNotice))
		m.color = false
		require.Equal(t, ansi.Strip(view), m.View().Content)
		m.color = true
		m.focusSection(2)
		m.settingsExpanded = true
		m.setting = 3
		m.beginInlineSpeed()
		m.speedInput.SetValue("8")
		audioReviewKey(m, tea.KeyEnter)
		require.NotEmpty(t, m.speedError)
		require.Contains(t, m.View().Content, m.warningStyle().Render(m.speedError))
		m.cancelInlineSpeed()
		m.settings.Model, m.settings.Voice = "tts-1", "marin"
		m.focusSection(0)
		require.Contains(t, m.View().Content, m.warningStyle().Render("Marin (unavailable)"))
	}
}

func TestAudioPlaybackActionsInheritForegroundAndSuppressBusySelection(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
			m := audioReviewModel(t)
			m.color, m.dark = true, dark
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.focusSection(4)
			m.resultAction = 1
			for _, recorded := range []bool{false, true} {
				if recorded {
					r := audioReviewResult("available-playback", false)
					m.result = &r
				}
				view := m.View().Content
				if !recorded {
					require.Contains(t, view, m.muted().Render("Your recording will appear here."))
					require.NotContains(t, ansi.Strip(view), "Play audio")
					require.NotContains(t, ansi.Strip(view), "Save audio…")
					continue
				}
				audioRequireTextForeground(t, view, "Play audio", lipgloss.NewStyle())
				require.Contains(t, ansi.Strip(view), "› Save audio…")
				m.color = false
				require.Equal(t, ansi.Strip(view), m.View().Content)
				m.color = true
			}
			m.busy = true
			require.Equal(t, "  "+m.muted().Render("Play audio"), m.playbackActionRow("Play audio", true), "busy controls must not claim focus or availability")
		}
	}
}

func TestAudioCompactPlayerTimingDistinguishesEmptyMeasuredAndUnknown(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, state := range []string{"empty", "measured", "unmeasured"} {
			t.Run(fmt.Sprintf("%s/dark_%t", state, dark), func(t *testing.T) {
				m := audioReviewModel(t)
				m.color, m.dark = true, dark
				m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
				m.focusSection(4)
				if state != "empty" {
					r := audioReviewResult("compact-timing", false)
					r.measured = state == "measured"
					r.position = 250 * time.Millisecond
					m.result = &r
				}
				view := m.View().Content
				switch state {
				case "empty":
					require.NotContains(t, ansi.Strip(view), "00:00.000 / 00:00.000")
					require.Contains(t, view, m.muted().Render("Your recording will appear here."))
				case "measured":
					require.Contains(t, view, m.accent().Bold(false).Render("00:00.250"))
					require.Contains(t, view, m.borderStyle().Render(" / "))
					require.Contains(t, view, m.muted().Render("00:01.000"))
				case "unmeasured":
					require.Contains(t, view, m.muted().Render("--:--.--- / --:--.---"))
					require.NotContains(t, ansi.Strip(view), "00:01.000")
					require.NotContains(t, ansi.Strip(view), "┃")
				}
				require.Contains(t, ansi.Strip(view), "Esc back")
				for _, text := range []string{"Play audio", "Save audio…"} {
					if state == "empty" {
						require.NotContains(t, ansi.Strip(view), text)
					} else {
						require.Contains(t, ansi.Strip(view), text)
					}
				}
				require.LessOrEqual(t, len(strings.Split(view, "\n")), 12)
				for _, row := range strings.Split(view, "\n") {
					require.LessOrEqual(t, ansi.StringWidth(row), 39)
				}
				m.color = false
				require.Equal(t, ansi.Strip(view), m.View().Content)
			})
		}
	}
}

func TestAudioPlayerStateColorHasLiteralMeaning(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, tc := range []struct {
			name     string
			position time.Duration
			playing  bool
			finished bool
			progress bool
			active   bool
			held     bool
		}{
			{name: "Ready"},
			{name: "Stopped", position: 250 * time.Millisecond, held: true},
			{name: "Paused", position: 250 * time.Millisecond, held: true},
			{name: "Playing", position: 250 * time.Millisecond, playing: true, active: true},
			{name: "Completed", position: time.Second, finished: true, progress: true},
			{name: "At end", position: time.Second, held: true},
		} {
			t.Run(fmt.Sprintf("%s/dark_%t", tc.name, dark), func(t *testing.T) {
				m := audioReviewModel(t)
				r := audioReviewResult("state", false)
				r.position, r.finished = tc.position, tc.finished
				r.resumable = tc.name == "Paused"
				m.result, m.focus, m.playing = &r, 4, tc.playing
				m.color, m.dark = true, dark
				style := m.muted()
				if tc.progress {
					style = m.playbackProgressStyle()
				} else if tc.active {
					style = m.accent().Bold(false)
				} else if tc.held {
					style = m.warningStyle()
				}
				require.Equal(t, style.Render(tc.name), m.playerStatus())
				view := m.View().Content
				require.Contains(t, view, style.Render(tc.name))
				m.color = false
				plain := m.View().Content
				require.Contains(t, plain, tc.name)
				require.NotContains(t, plain, "\x1b[38;")
				require.Equal(t, ansi.Strip(view), plain)
			})
		}
		m := audioReviewModel(t)
		m.color, m.dark = true, dark
		require.Nil(t, m.result)
		require.Equal(t, m.muted().Render("Ready"), m.playerStatus(), "an empty player must not claim generated audio")
	}
}

func TestAudioActionColorsRespectSelectionAndMonochrome(t *testing.T) {
	for _, width := range []int{80, 137} {
		for _, dark := range []bool{false, true} {
			m := audioReviewModel(t)
			m.color, m.dark = true, dark
			m.Update(tea.WindowSizeMsg{Width: width, Height: 32})
			m.openActions()
			m.failure.Summary = "Test failure"
			view := m.View().Content
			require.Contains(t, view, m.warningStyle().Render("Clear form"))
			require.Contains(t, view, m.failureStyle().Render("Last error"))
			m.moreChoice = 2
			view = m.View().Content
			require.Contains(t, ansi.Strip(view), "› Clear form")
			require.NotContains(t, view, m.warningStyle().Render("Clear form"))
			m.color = false
			require.Equal(t, ansi.Strip(view), m.View().Content)
		}
	}
}

func TestAudioPlayerControlsAndSavedFilenameStayVisibleAcrossStates(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		for _, measured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/measured_%t", size[0], size[1], measured), func(t *testing.T) {
				m := audioReviewModel(t)
				r := audioReviewResult("placement", false)
				r.measured = measured
				r.savedPaths = []string{"/exports/" + strings.Repeat("界👨‍👩‍👧‍👦", 30) + "/clip.wav"}
				m.result, m.focus = &r, 4
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				var savedColumn, saveColumn int
				for state := range 3 {
					m.playing, r.finished = state == 1, state == 2
					view := ansi.Strip(m.View().Content)
					require.Contains(t, view, "clip.wav")
					require.Contains(t, view, "Saved: …")
					require.Contains(t, view, "Save audio…")
					require.Contains(t, view, "Esc back")
					rows := strings.Split(view, "\n")
					require.LessOrEqual(t, len(rows), size[1])
					for _, row := range rows {
						require.LessOrEqual(t, ansi.StringWidth(row), size[0]-1)
						for _, item := range []struct {
							text   string
							column *int
						}{{"Saved:", &savedColumn}, {"Save audio…", &saveColumn}} {
							if index := strings.Index(row, item.text); index >= 0 {
								column := ansi.StringWidth(row[:index])
								if state == 0 {
									*item.column = column
								} else {
									require.Equal(t, *item.column, column, "%s shifted with playback state", item.text)
								}
							}
						}
					}
				}
			})
		}
	}
}

func TestAudioPlayerPathSuffixKeepsDisplaySequencesAndEscapesControls(t *testing.T) {
	for _, prefix := range []string{strings.Repeat("界", 30), strings.Repeat("e\u0301", 50), strings.Repeat("👨‍👩‍👧‍👦", 30), strings.Repeat("1️⃣", 30)} {
		for _, width := range []int{9, 23, 24, 37} {
			path := audioPathSuffix(prefix+"/clip.wav", width)
			require.True(t, strings.HasSuffix(path, "clip.wav"))
			require.True(t, strings.HasPrefix(path, "…"))
			cells := 0
			for remaining := path; len(remaining) > 0; {
				_, width, consumed := cliterminal.Sequence(remaining)
				cells += width
				remaining = remaining[consumed:]
			}
			require.LessOrEqual(t, cells, width)
		}
	}
	const short = "/exports/clip.wav"
	require.Equal(t, short, audioPathSuffix(short, 40))
	require.Empty(t, audioPathSuffix(short, 0))
	require.Equal(t, "…", audioPathSuffix(short, 1))
	path := audioPathSuffix("/exports/\x1b[2J\r\n\t\u202e/clip.wav", 80)
	for _, control := range []string{"\x1b", "\r", "\n", "\t", "\u202e"} {
		require.NotContains(t, path, control)
	}
	require.Contains(t, path, `\n\t\u202e`)
	require.True(t, strings.HasSuffix(path, "clip.wav"))
}

// Inspect actual visible letters, independent of style-run padding and grouping.
func audioRequireTextForeground(t *testing.T, view, text string, style lipgloss.Style) {
	t.Helper()
	want := audioRenderedCells(t, style.Render("x"))[0].foreground
	for _, row := range strings.Split(view, "\n") {
		plain := ansi.Strip(row)
		index := strings.Index(plain, text)
		if index < 0 {
			continue
		}
		start := len([]rune(plain[:index]))
		cells := audioRenderedCells(t, row)
		for _, cell := range cells[start : start+len([]rune(text))] {
			require.Equal(t, want, cell.foreground)
		}
		return
	}
	t.Fatalf("text %q is missing", text)
}

func TestAudioDesignSystemReservesSuccessColorForCompletedPlayback(t *testing.T) {
	for _, dark := range []bool{false, true} {
		green := "35;107;67"
		if dark {
			green = "141;201;160"
		}
		runs := regexp.MustCompile(`\x1b\[(?:[0-9]+;)*38;2;` + green + `m([^\x1b]*)\x1b\[m`)
		for _, size := range [][2]int{{40, 12}, {101, 31}} {
			for _, state := range []string{"ready", "saved", "stopped", "seek-end", "playing", "busy", "completed"} {
				m := audioReviewModel(t)
				r := audioReviewResult("semantic-green", false)
				m.result, m.focus = &r, 4
				m.color, m.dark = true, dark
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				switch state {
				case "saved":
					r.savedPaths = []string{"/exports/confirmed.wav"}
				case "stopped":
					r.position = 250 * time.Millisecond
				case "seek-end":
					r.position = time.Second
				case "playing":
					m.playing = true
				case "busy":
					m.busy = true
				case "completed":
					r.position, r.finished = time.Second, true
				}
				view := m.View().Content
				greenRuns := runs.FindAllStringSubmatch(view, -1)
				if state != "completed" {
					require.Empty(t, greenRuns, "%s must not imply successful completion", state)
				} else {
					require.Len(t, greenRuns, 2)
					for _, run := range greenRuns {
						require.Contains(t, []string{"Completed", "┷"}, run[1])
					}
				}
				m.color = false
				require.Equal(t, ansi.Strip(view), m.View().Content)
			}
		}
	}
}

func TestAudioDesignSystemSelectionCoversFixedActionBlocks(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, size := range [][2]int{{40, 12}, {101, 31}} {
			m := audioReviewModel(t)
			m.color, m.dark = true, dark
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			r := audioReviewResult("selected-player-actions", false)
			m.result = &r
			m.focusSection(4)
			for action, label := range []string{"Play audio", "Save audio…"} {
				m.resultAction = action
				width := []int{15, 14}[action]
				view := m.View().Content
				require.Contains(t, view, m.selected("› "+label, width), "the full reserved action area must receive selection")
				if m.framed() {
					for _, row := range strings.Split(view, "\n") {
						if !strings.Contains(ansi.Strip(row), "Save audio…") {
							continue
						}
						cells := audioRenderedCells(t, row)
						start := []int{3, 19}[action]
						require.Equal(t, '›', cells[start].glyph)
						require.NotEmpty(t, cells[start].background)
						require.Empty(t, cells[start-1].background)
						require.Empty(t, cells[start+width].background)
						require.Equal(t, ' ', cells[18].glyph)
						require.Empty(t, cells[18].background, "the cell between Play and Save stays unpainted")
					}
				}
				m.color = false
				require.Equal(t, ansi.Strip(view), m.View().Content)
				m.color = true
			}
			m.focusSection(2)
			m.setting = audioSettingHistory
			view := m.View().Content
			require.NotContains(t, view, m.accent().Bold(false).Underline(true).Render("Generate speech"))
			require.NotContains(t, ansi.Strip(view), "› Generate speech")
			require.Contains(t, ansi.Strip(view), "› History")
			m.busy = true
			view = ansi.Strip(m.View().Content)
			for _, label := range []string{"Generate speech", "History", "More actions…", "Play audio", "Save audio…"} {
				require.NotContains(t, view, "› "+label)
			}
		}
	}
}
