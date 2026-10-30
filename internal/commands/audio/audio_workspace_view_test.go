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

func TestAudioWorkspaceActionFocusDoesNotMovePlayback(t *testing.T) {
	for _, width := range []int{68, 69, 70, 71, 72, 73, 74, 101} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		var initial [2]int
		for i, target := range [][2]int{{0, 0}, {2, 4}, {2, audioSettingHistory}, {2, audioSettingMore}, {4, 0}} {
			m.focusSection(target[0])
			m.setting = target[1]
			view := ansi.Strip(m.View().Content)
			var positions [2]int
			for row, line := range strings.Split(view, "\n") {
				for item, label := range []string{"More actions…", "No recording yet"} {
					if strings.Contains(line, label) {
						positions[item] = row
					}
				}
			}
			for _, row := range positions {
				require.Positive(t, row, "each control must remain visible")
			}
			if i == 0 {
				initial = positions
			} else {
				require.Equal(t, initial, positions, "focus must not reflow the workspace at width %d", width)
			}
			assertAudioWorkspaceBounds(t, view, [2]int{width, 24})
		}
	}
}

func TestAudioWorkspaceKeepsDraftAndRecordingVisibleAcrossFocus(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {101, 29}} {
		var initial [3]int
		for _, focus := range []int{0, 1, 2, 4} {
			t.Run(fmt.Sprintf("%dx%d/focus_%d", size[0], size[1], focus), func(t *testing.T) {
				m := audioReviewModel(t)
				r := audioReviewResult("retained", false)
				r.entry.Settings.Input = "Retained recording."
				r.entry.Settings.Voice, r.entry.Settings.Format = "alloy", "mp3"
				r.entry.Bytes = 123456
				r.savedPaths = []string{"/exports/" + strings.Repeat("界", 60) + "/clip.mp3"}
				m.result = &r
				m.input.SetValue("Editable draft.")
				m.instructions.SetValue("Keep this instruction.")
				m.focusSection(focus)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				view := ansi.Strip(m.View().Content)
				for _, text := range []string{"Text to speech", "Editable draft.", "Instructions", "Settings", "Voice", "gpt-4o-mini-tts", "WAV", "1.00×", "Generate speech", "History", "More actions", "Alloy  MP3  120.6 KiB", "Recording: Retained recording.", "┠", "00:00.000 / 00:01.000", "Play audio", "Save audio…", "Saved: …", "clip.mp3"} {
					require.Contains(t, view, text)
				}
				require.Contains(t, strings.Split(view, "\n")[1], "Text to speech")
				require.Contains(t, strings.Split(view, "\n")[2], "Editable draft.")
				rows := strings.Split(view, "\n")
				positions := [3]int{-1, -1, -1}
				for index, row := range rows {
					if strings.Contains(row, "Alloy  MP3") {
						require.Contains(t, row, "Ready", "player state must share the recording header")
						require.Greater(t, strings.Index(row, "Ready"), strings.Index(row, "120.6 KiB"))
					}
					if strings.Contains(row, "00:00.000 / 00:01.000") {
						positions[0] = index
						if size[0] == 60 {
							require.NotContains(t, row, "Play audio")
							require.NotContains(t, row, "Save audio…")
							require.Less(t, index+1, len(rows))
							require.Contains(t, rows[index+1], "Play audio")
							require.Contains(t, rows[index+1], "Save audio…")
						} else {
							require.Contains(t, row, "Play audio")
							require.Contains(t, row, "Save audio…")
						}
						require.NotContains(t, row, "Ready", "player state belongs in the metadata header")
					}
					if strings.Contains(row, "Play audio") {
						positions[1] = index
						require.Contains(t, row, "Save audio…")
						prefix := strings.SplitN(row, "Play audio", 2)[0]
						require.Equal(t, 5, ansi.StringWidth(prefix), "Play follows its two-cell focus prefix at the rail origin")
						require.NotContains(t, row, "Ready", "player state belongs in the metadata header")
					}
					if strings.Contains(row, "Save audio…") {
						positions[2] = index
					}
				}
				for _, position := range positions {
					require.Positive(t, position)
				}
				if focus == 0 {
					initial = positions
				} else {
					require.Equal(t, initial, positions, "focus must not move timing or playback controls")
				}
				require.NotContains(t, view, "View command")
				require.NotContains(t, view, "printf %s")
				require.NotContains(t, view, "OPENAI_UNTRUSTED_STDIN")
				if focus == 4 {
					require.Contains(t, view, "› Play audio")
					require.Contains(t, view, "Space play")
				} else {
					require.NotContains(t, view, "› Play audio")
					require.NotContains(t, view, "Space play")
					require.Contains(t, view, "Ctrl+S Save")
					require.NotContains(t, view, "Ctrl+O Audio")
				}
				assertAudioWorkspaceBounds(t, view, size)
			})
		}
	}
}

func TestAudioWorkspaceGroupsWrappedTextWithoutDivider(t *testing.T) {
	for _, width := range []int{60, 101} {
		m := audioReviewModel(t)
		r := audioReviewResult("wrapped-text", false)
		m.result = &r
		text := strings.Repeat("界🙂 wrapped text ", 18) + "TEXT_TAIL"
		m.input.SetValue(text)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 29})
		m.input.MoveToEnd()
		line, column := m.input.Line(), m.input.Column()
		view := ansi.Strip(m.View().Content)
		rows := strings.Split(view, "\n")
		require.Greater(t, m.input.Height(), 1, "the probe must exercise visible wrapping")
		require.Equal(t, text, m.input.Value())
		require.Equal(t, line, m.input.Line())
		require.Equal(t, column, m.input.Column())
		tailRow := -1
		for index, row := range rows {
			if strings.Contains(row, "TEXT_TAIL") {
				tailRow = index
			}
		}
		require.NotEqual(t, -1, tailRow, "wrapped draft tail must remain visible")
		require.Empty(t, strings.Trim(rows[tailRow+1], " │"), "space separates Text from Instructions")
		require.Contains(t, rows[tailRow+2], "Instructions")
		for _, item := range []string{"Settings", "Voice", "Audio", "Play audio", "Save audio…"} {
			require.Contains(t, view, item)
		}
		assertAudioWorkspaceBounds(t, view, [2]int{width, 29})
	}
}

func TestAudioWorkspaceKeepsControlsUnderCrowdedStates(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {101, 29}} {
		for _, name := range []string{"long draft", "invalid speed", "unsupported instructions", "unmeasured", "busy unsupported"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], name), func(t *testing.T) {
				m := audioReviewModel(t)
				r := audioReviewResult("crowded", false)
				r.entry.Settings.Input = "Retained recording."
				r.savedPaths = []string{"/exports/" + strings.Repeat("👨‍👩‍👧‍👦界", 40) + "/clip.wav"}
				m.result = &r
				m.input.SetValue(strings.Repeat("Long draft 世界 ", 40) + "\n\nFinal draft line.")
				m.instructions.SetValue(strings.Repeat("Read naturally. ", 30) + "\nSecond instruction.\nThird instruction.")
				m.focusSection(1)
				switch name {
				case "invalid speed":
					m.focusSection(2)
					m.setting = 3
					m.settingsExpanded = true
					m.beginInlineSpeed()
					m.speedInput.SetValue("nope")
					m.speedError = "Enter a speed from 0.25 to 4.00."
				case "unsupported instructions", "busy unsupported":
					m.settings.Model = "tts-1"
					m.focusSection(4)
					if name == "busy unsupported" {
						m.busy, m.started = true, time.Now()
						m.waveformFocused = true
					}
				case "unmeasured":
					r.measured, r.analysis.Peaks = false, nil
					m.focusSection(4)
				}
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				view := ansi.Strip(m.View().Content)
				for _, text := range []string{"Settings", "Voice", "Audio", "Recording: Retained recording.", "Play audio", "Save audio…", "Saved: …", "clip.wav"} {
					require.Contains(t, view, text)
				}
				switch name {
				case "invalid speed":
					require.Contains(t, view, m.speedError)
					require.Contains(t, view, "nope")
					require.Contains(t, view, "Enter apply")
					require.Contains(t, view, "Esc cancel")
				case "unmeasured":
					require.Contains(t, view, "Preview unavailable")
					require.NotContains(t, view, "┃")
				case "unsupported instructions", "busy unsupported":
					require.Contains(t, view, "Not used by this model")
				}
				if m.busy {
					require.Contains(t, view, "Working…")
					require.Contains(t, view, "Ctrl+C cancel")
					require.NotContains(t, view, "› Play audio")
					require.NotContains(t, view, "› Save audio")
					require.NotContains(t, view, "Space play")
				}
				assertAudioWorkspaceBounds(t, view, size)
			})
		}
	}
}

func TestAudioWorkspaceDoesNotInventAudioBeforeGeneration(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {101, 29}} {
		m := audioReviewModel(t)
		m.input.SetValue("A future recording.")
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "No recording yet")
		require.NotContains(t, view, "Generate speech to create audio.")
		require.NotContains(t, view, "┷")
		require.NotContains(t, view, "┃")
		require.Contains(t, view, "00:00.000 / 00:00.000")
		require.Contains(t, view, "Play audio")
		require.Contains(t, view, "Save audio…")
		require.NotContains(t, view, "printf %s")
		assertAudioWorkspaceBounds(t, view, size)
	}
}

func TestAudioEmptyRecordingAndUnknownTimingRemainDistinct(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.focusSection(4)
		m.color, m.dark = true, true
		view := m.View().Content
		if m.framed() {
			require.Contains(t, view, m.muted().Render("00:00.000 / 00:00.000"))
			require.Contains(t, view, m.muted().Render("No recording yet"))
		} else {
			require.NotContains(t, ansi.Strip(view), "00:00.000 / 00:00.000")
			require.Contains(t, view, m.muted().Render("Your recording will appear here."))
		}
		require.NotContains(t, view, "┃")
		r := audioReviewResult("unknown-timing", false)
		r.measured = false
		m.result = &r
		view = m.View().Content
		require.Contains(t, view, m.muted().Render("--:--.--- / --:--.---"))
		require.Contains(t, ansi.Strip(view), "Preview unavailable")
		require.NotContains(t, ansi.Strip(view), "00:00.000 / 00:00.000")
		require.NotContains(t, view, "┃")
	}
}

func TestAudioWorkspaceExplainsChangedDraftWithoutRelabelingRecording(t *testing.T) {
	for _, field := range []string{"input", "instructions", "model", "voice", "format", "speed", "preview", "busy"} {
		t.Run(field, func(t *testing.T) {
			m := audioReviewModel(t)
			r := audioReviewResult("previous-generation", false)
			m.result, m.settings = &r, r.entry.Settings
			m.input.SetValue(r.entry.Settings.Input)
			m.instructions.SetValue(r.entry.Settings.Instructions)
			m.color, m.dark = true, true
			require.NotContains(t, ansi.Strip(m.View().Content), "Draft changed")
			switch field {
			case "input":
				m.input.SetValue("A new draft")
			case "instructions":
				m.instructions.SetValue("A different delivery")
			case "model":
				m.settings.Model = "tts-1"
			case "voice":
				m.settings.Voice = "ash"
			case "format":
				m.settings.Format = "mp3"
			case "speed":
				m.settings.Speed = .75
			case "preview":
				m.settings.Preview = true
			case "busy":
				m.input.SetValue("A new draft during generation")
				m.busy, m.started = true, time.Now()
			}
			view := m.View().Content
			if field == "preview" {
				require.NotContains(t, ansi.Strip(view), "Draft changed")
			} else {
				require.Contains(t, view, m.warningStyle().Render("Draft changed. Showing previous audio."))
			}
			require.Contains(t, ansi.Strip(view), "Coral  WAV")
			if field == "input" {
				require.Contains(t, ansi.Strip(view), "Recording: "+r.entry.Settings.Input)
			}
		})
	}
	m := audioReviewModel(t)
	r := audioReviewResult("unsupported-instructions", false)
	r.entry.Settings.Model, r.entry.Settings.Instructions = "tts-1", ""
	m.result, m.settings = &r, r.entry.Settings
	m.input.SetValue(r.entry.Settings.Input)
	m.instructions.SetValue("Ignored instruction draft")
	require.NotContains(t, ansi.Strip(m.View().Content), "Draft changed")
}

func TestAudioWorkspaceSeparatesMeasuredWaveformsOnlyWhenRoomPermits(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {80, 24}, {101, 29}, {101, 30}, {120, 40}} {
		for _, state := range []string{"empty", "unmeasured", "measured"} {
			m := audioReviewModel(t)
			bottomLabel := "No recording yet"
			if state != "empty" {
				r := audioReviewResult("framed-player", false)
				r.entry.Settings = m.effectiveSettings()
				r.measured = state == "measured"
				m.result = &r
				bottomLabel = "Preview unavailable"
				if r.measured {
					bottomLabel = "┷"
				}
			}
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.focusSection(4)
			view := ansi.Strip(m.View().Content)
			rows := strings.Split(view, "\n")
			bottom, timing, actions := -1, -1, -1
			for index, row := range rows {
				if strings.Contains(row, bottomLabel) {
					bottom = index
				}
				if strings.Contains(row, " / ") {
					timing = index
				}
				if strings.Contains(row, "Play audio") {
					actions = index
				}
				if strings.Contains(row, "┃") {
					require.True(t, strings.HasPrefix(row, "│  "), "waveform begins at the shared content gutter")
					require.NotContains(t, row, "╭")
				}
			}
			require.Positive(t, bottom)
			if state == "empty" {
				require.Greater(t, timing, bottom)
				require.Greater(t, actions, bottom)
				audioReviewKey(m, tea.KeySpace)
				require.Contains(t, ansi.Strip(m.View().Content), audioNoGenerationNotice)
				require.Contains(t, ansi.Strip(m.View().Content), "Ctrl+G")
				continue
			}
			require.Positive(t, timing)
			require.Positive(t, actions)
			gapRows := rows[bottom+1 : min(timing, actions)]
			if state == "measured" {
				require.LessOrEqual(t, len(gapRows), 2, "only the ruler and one blank row may follow the rail")
				for _, gap := range gapRows {
					if strings.Trim(gap, " │") != "" {
						require.Contains(t, gap, "00:00")
						require.Contains(t, gap, "00:01")
					}
				}
				if size[1] >= 36 {
					require.Empty(t, strings.Trim(rows[bottom-1], " │"))
				}
				m.result.savedPaths = []string{"/synthetic/clip.wav"}
				saved := strings.Split(ansi.Strip(m.View().Content), "\n")
				require.Equal(t, rows[:bottom+1], saved[:bottom+1], "a receipt must preserve the measured rows and rail")
				require.Equal(t, rows[actions], saved[actions], "a receipt must not move the transport")
				require.Contains(t, saved[actions+1], "Saved: /synthetic/clip.wav")
			}
			require.NotContains(t, view, "Save to")
			assertAudioWorkspaceBounds(t, view, size)
		}
	}
}

func TestAudioWorkspaceToolbarKeepsExactTimingAndBounds(t *testing.T) {
	// Short timers wrap below 64 columns. Wider workspaces hold column 40.
	for _, tc := range []struct{ width, height, shortStart int }{
		{60, 24, -1}, {63, 24, -1}, {64, 24, 39}, {66, 24, 39}, {68, 24, 39}, {101, 29, 39}, {186, 40, 39},
	} {
		size := [2]int{tc.width, tc.height}
		for _, duration := range []float64{1.35, 9000000000} {
			m := audioReviewModel(t)
			r := audioReviewResult("toolbar", false)
			r.analysis.Duration = duration
			r.position = 644 * time.Millisecond
			if duration > 2 {
				r.position = 8999999999 * time.Second
			}
			m.result, m.focus = &r, 4
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.color, m.dark = true, true
			elapsed, total := audioDuration(r.playbackPosition().Seconds()), audioDuration(duration)
			colored := m.View().Content
			view := ansi.Strip(colored)
			require.Contains(t, view, elapsed+" / "+total, "exact timing must survive the narrow fallback")
			header := m.workspaceAudioHeader()[0]
			audioRequireTextForeground(t, header, "Coral", m.primaryText())
			audioRequireTextForeground(t, header, "WAV", m.muted())
			audioRequireTextForeground(t, header, audioFileSize(r.entry.Bytes), m.muted())
			require.Contains(t, colored, m.accent().Bold(false).Render(elapsed))
			require.Contains(t, colored, m.borderStyle().Render(" / "))
			require.Contains(t, colored, m.muted().Render(total))
			require.NotContains(t, colored, m.playbackProgressStyle().Render(elapsed))
			rows := strings.Split(view, "\n")
			left, right := -1, -1
			for _, row := range rows {
				if !strings.Contains(row, "┷") {
					continue
				}
				left = ansi.StringWidth(row[:strings.IndexAny(row, "├┷")])
				right = ansi.StringWidth(row[:strings.LastIndexAny(row, "┤┷")])
				break
			}
			require.Positive(t, left)
			require.Greater(t, right, left)
			for i, row := range rows {
				if strings.Contains(row, "Play audio") {
					require.Contains(t, row, "Save audio…")
					playStart := ansi.StringWidth(row[:strings.Index(row, "Play audio")])
					saveStart := ansi.StringWidth(row[:strings.Index(row, "Save audio…")])
					require.Equal(t, left+2, playStart, "the Play selection begins at the zero boundary")
					require.Equal(t, 21, saveStart, "Save aligns with the form values (column 22)")
					if duration < 2 && tc.shortStart >= 0 {
						require.Contains(t, row, elapsed+" / "+total)
					}
				}
				if strings.Contains(row, elapsed+" / "+total) {
					timeStart := ansi.StringWidth(row[:strings.Index(row, elapsed)])
					timeWidth := ansi.StringWidth(elapsed + " / " + total)
					wantStart := tc.shortStart
					if duration > 2 && tc.width < 101 {
						wantStart = -1 // These widths cannot hold the 35-cell clock beside Save.
					}
					if wantStart >= 0 {
						require.Contains(t, row, "Save audio…")
						require.Equal(t, wantStart, timeStart, "the clock stays beside the actions")
					} else {
						require.Equal(t, right, timeStart+timeWidth-1, "the wrapped timer ends below the final tick")
						require.NotContains(t, row, "Play audio", "narrow timing must not crowd action reservations")
						require.Contains(t, rows[i+1], "Play audio")
					}
				}
				if strings.Contains(row, "Generate speech") && !m.inlineActions() {
					require.Contains(t, row, "More actions…")
					require.NotContains(t, view, "View command")
				}
			}
			assertAudioWorkspaceBounds(t, view, size)
			m.color = false
			require.Equal(t, view, m.View().Content)
		}
	}
}

func TestAudioWorkspaceLongRecordingKeepsToolbarStableWhileSeeking(t *testing.T) {
	for _, width := range []int{60, 101} {
		m := audioReviewModel(t)
		r := audioReviewResult("long-toolbar", false)
		r.analysis.Duration = 90000
		m.result, m.focus = &r, 4
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		positions := func() [2][2]int {
			var found [2][2]int
			label := "Play audio"
			if r.position == r.seekDuration() {
				label = "At end"
			}
			for row, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
				if !strings.Contains(line, "Save audio…") {
					continue
				}
				for i, text := range []string{label, "Save audio…"} {
					column := strings.Index(line, text)
					require.GreaterOrEqual(t, column, 0)
					found[i] = [2]int{row, ansi.StringWidth(line[:column])}
				}
			}
			require.Positive(t, found[0][0])
			return found
		}
		initial := positions()
		total := audioDuration(r.analysis.Duration)
		timerEnd := 39 + 2*ansi.StringWidth(total) + 3 // Exclusive edge of the widest timer.
		if width == 60 {
			timerEnd = 56 // Exclusive edge under the final tick.
		}
		for _, seconds := range []int{1, 6000, 60000, 89999, 90000} {
			r.position = time.Duration(seconds) * time.Second
			require.Equal(t, initial, positions(), "timestamp growth must not move actions")
			view := ansi.Strip(m.View().Content)
			timing := audioDuration(r.position.Seconds()) + " / " + total
			require.Contains(t, view, timing)
			for _, line := range strings.Split(view, "\n") {
				if index := strings.Index(line, timing); index >= 0 {
					require.Equal(t, timerEnd, ansi.StringWidth(line[:index])+ansi.StringWidth(timing))
				}
			}
			assertAudioWorkspaceBounds(t, view, [2]int{width, 24})
		}
	}
}

func TestAudioWorkspacePreservesBlankEditorRowsAndScopesWaveformFocus(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("retained", false)
	m.result = &r
	m.input.SetValue("First editor line.\n\nLast editor line.")
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 29})
	m.waveformFocused = true
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "First editor line.")
	require.Contains(t, view, "Last editor line.")
	rows := strings.Split(view, "\n")
	for index, row := range rows {
		if strings.Contains(row, "First editor line.") {
			script := ansi.Cut(rows[index+1], 1, 1+m.composerWidth())
			require.Empty(t, strings.Trim(script, " │"), "an intentional editor line was removed")
			require.Contains(t, rows[index+2], "Last editor line.")
		}
	}
	for _, row := range rows {
		if strings.ContainsAny(row, "┃┠") {
			require.NotContains(t, row, "›", "waveform must not claim Text focus")
		}
	}
	m.focusSection(4)
	m.waveformFocused = true
	view = ansi.Strip(m.View().Content)
	marked := false
	for _, row := range strings.Split(view, "\n") {
		marked = marked || strings.Contains(row, "┠") && strings.Contains(row, "›")
	}
	require.True(t, marked)
	require.Contains(t, view, "←→ seek")
	for _, dark := range []bool{false, true} {
		m.color, m.dark = true, dark
		colored := m.View().Content
		m.color = false
		plain := m.View().Content
		require.Equal(t, ansi.Strip(colored), ansi.Strip(plain))
		require.NotContains(t, plain, "\x1b[38;")
		require.NotContains(t, plain, "\x1b[48;")
	}
}

func TestAudioWorkspaceEscapesRetainedMetadataAndPaths(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("untrusted-metadata", false)
	r.entry.Settings.Input = "Retained\n\t\x1b[2J\u202e recording."
	r.entry.Settings.Voice = strings.Repeat("coral", 40) + "\x1b[2J"
	r.entry.Settings.Format = "wav\u202e"
	r.savedPaths = []string{"/exports/\x1b[2J\n\t\u202e/" + strings.Repeat("界", 40) + "/clip.wav"}
	m.result = &r
	m.input.SetValue("A newer draft.")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	view := m.View().Content
	for _, control := range []string{"\x1b[2J", "\u202e", "\t"} {
		require.NotContains(t, view, control)
	}
	require.Contains(t, view, "Recording:")
	require.Contains(t, view, "clip.wav")
	assertAudioWorkspaceBounds(t, ansi.Strip(view), [2]int{60, 24})
}

func TestAudioWorkspaceResizeKeepsRetainedResultAndEditingTail(t *testing.T) {
	for _, width := range []int{60, 101} {
		for _, focus := range []int{0, 1} {
			t.Run(fmt.Sprintf("width_%d/focus_%d", width, focus), func(t *testing.T) {
				m := audioReviewModel(t)
				r := audioReviewResult("retained-resize", false)
				m.result = &r
				m.input.SetValue(strings.Repeat("界🙂e\u0301 wrapped text ", 25) + "\nMiddle text 世界.\nTEXT_TAIL")
				m.instructions.SetValue(strings.Repeat("声🙂e\u0301 spoken text ", 25) + "\nMiddle instructions 世界.\nINSTRUCTION_TAIL")
				m.focusSection(focus)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				m.input.MoveToEnd()
				m.instructions.MoveToEnd()
				input, instructions := m.input.Value(), m.instructions.Value()
				inputLine, inputColumn := m.input.Line(), m.input.Column()
				instructionLine, instructionColumn := m.instructions.Line(), m.instructions.Column()
				tail := []string{"TEXT_TAIL", "INSTRUCTION_TAIL"}[focus]
				for _, height := range []int{24, 26, 28, 24} {
					m.Update(tea.WindowSizeMsg{Width: width, Height: height})
					view := ansi.Strip(m.View().Content)
					require.Equal(t, input, m.input.Value())
					require.Equal(t, instructions, m.instructions.Value())
					require.Equal(t, inputLine, m.input.Line())
					require.Equal(t, inputColumn, m.input.Column(), "resize moved the Text caret")
					require.Equal(t, instructionLine, m.instructions.Line())
					require.Equal(t, instructionColumn, m.instructions.Column(), "resize moved the Instructions caret")
					require.Equal(t, 3, m.input.Height())
					require.Equal(t, 3, m.instructions.Height())
					require.Equal(t, focus, m.focus)
					require.Same(t, &r, m.result)
					require.Contains(t, view, tail, "focused editor tail must survive height changes")
					require.Contains(t, view, "Play audio")
					require.Contains(t, view, "Save audio…")
					assertAudioWorkspaceBounds(t, view, [2]int{width, height})
				}
			})
		}
	}
}

func assertAudioWorkspaceBounds(t *testing.T, view string, size [2]int) {
	t.Helper()
	rows := strings.Split(view, "\n")
	require.Len(t, rows, size[1])
	for _, row := range rows {
		require.LessOrEqual(t, ansi.StringWidth(row), size[0]-1)
	}
}
