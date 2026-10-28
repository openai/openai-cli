package audio

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func audioResultNavigationModel(t *testing.T, width, height int) *audioWorkbench {
	t.Helper()
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	result := audioReviewResult("navigation-result", false)
	// Keep one second per bar at the tested geometry.
	result.analysis.Duration = float64(audioWaveformBars(m.waveformWidth()))
	result.position = 3 * time.Second
	m.Update(audioWorkMessage{kind: "generate", result: result})
	m.lastFinished = time.Now().Add(-time.Second)
	require.Equal(t, 4, m.focus)
	require.Zero(t, m.resultAction)
	require.False(t, m.waveformFocused)
	return m
}

func audioResultNavigationPlayback(t *testing.T, m *audioWorkbench) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	m.playing, m.playID, m.note = true, 42, audioPlayingNote
	m.playCancel = cancel
	return ctx
}

func TestAudioResultNavigationActionsPreservePlayback(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := audioResultNavigationModel(t, size[0], size[1])
			playing := audioResultNavigationPlayback(t, m)
			result, entry := m.result, m.result.entry
			for _, step := range []struct {
				key    tea.KeyPressMsg
				action int
			}{
				{tea.KeyPressMsg{Code: tea.KeyRight}, 1},
				{tea.KeyPressMsg{Code: tea.KeyRight}, 1},
				{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}, 1},
				{tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}, 1},
				{tea.KeyPressMsg{Code: tea.KeyLeft}, 0},
				{tea.KeyPressMsg{Code: tea.KeyLeft}, 0},
				{tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}, 0},
				{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}, 0},
			} {
				_, command := m.Update(step.key)
				require.Nil(t, command)
				require.Equal(t, step.action, m.resultAction)
				require.False(t, m.waveformFocused)
				require.Equal(t, 4, m.focus)
				require.Same(t, result, m.result)
				require.Equal(t, entry, m.result.entry)
				require.Equal(t, 3*time.Second, m.result.position)
				require.Equal(t, uint64(42), m.playID)
				require.True(t, m.playing)
				require.NoError(t, playing.Err(), "choosing an action canceled playback")
				require.False(t, m.busy)
			}
		})
	}
}

func TestAudioResultNavigationWaveformReturnsToSelectedAction(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, action := range []int{0, 1} {
			t.Run(fmt.Sprintf("%dx%d/action_%d", size[0], size[1], action), func(t *testing.T) {
				m := audioResultNavigationModel(t, size[0], size[1])
				playing := audioResultNavigationPlayback(t, m)
				if action == 1 {
					require.Nil(t, audioReviewKey(m, tea.KeyRight))
				}
				require.Nil(t, audioReviewKey(m, tea.KeyUp))
				require.True(t, m.waveformFocused)
				require.Equal(t, action, m.resultAction)
				require.True(t, m.playing)
				require.NoError(t, playing.Err())
				require.Equal(t, 3*time.Second, m.result.position)

				for _, step := range []struct {
					key      rune
					modified bool
					position time.Duration
				}{
					{tea.KeyRight, false, 4 * time.Second},
					{tea.KeyRight, true, 9 * time.Second},
					{tea.KeyLeft, true, 4 * time.Second},
					{tea.KeyLeft, false, 3 * time.Second},
				} {
					require.Nil(t, audioSeekKey(m, step.key, step.modified))
					require.True(t, m.waveformFocused)
					require.Equal(t, action, m.resultAction)
					require.Equal(t, step.position, m.result.position)
					require.False(t, m.playing)
					require.ErrorIs(t, playing.Err(), context.Canceled)
					require.Equal(t, uint64(43), m.playID, "seeking canceled the same playback more than once")
					require.False(t, m.busy)
				}
				require.Nil(t, audioReviewKey(m, tea.KeyDown))
				require.False(t, m.waveformFocused)
				require.Equal(t, action, m.resultAction, "Down changed the action selected before waveform focus")
				require.Equal(t, 3*time.Second, m.result.position)
			})
		}
	}
}

func TestAudioResultNavigationHomeEndAndReentryPreserveTarget(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, reentry := range []string{"shift-tab", "ctrl-o"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], reentry), func(t *testing.T) {
				m := audioResultNavigationModel(t, size[0], size[1])
				playing := audioResultNavigationPlayback(t, m)
				for _, step := range []struct {
					key      rune
					action   int
					waveform bool
				}{
					{tea.KeyRight, 1, false},
					{tea.KeyUp, 1, true},
					{tea.KeyHome, 0, false},
					{tea.KeyUp, 0, true},
					{tea.KeyEnd, 1, false},
					{tea.KeyUp, 1, true},
				} {
					require.Nil(t, audioReviewKey(m, step.key))
					require.Equal(t, step.action, m.resultAction)
					require.Equal(t, step.waveform, m.waveformFocused)
					require.True(t, m.playing)
					require.NoError(t, playing.Err())
					require.Equal(t, 3*time.Second, m.result.position)
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				require.Equal(t, 0, m.focus, "Tab did not leave the result")
				key := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
				if reentry == "ctrl-o" {
					key = tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
				}
				m.Update(key)
				require.Equal(t, 4, m.focus)
				require.False(t, m.waveformFocused, "reentering the result retained hidden waveform focus")
				require.Equal(t, 1, m.resultAction)
				require.Equal(t, 3*time.Second, m.result.position)
				require.True(t, m.playing)
				require.NoError(t, playing.Err())
				require.Equal(t, uint64(42), m.playID)
				require.False(t, m.busy)
			})
		}
	}
}

func TestAudioResultNavigationGuardsPreserveSelectionAndPlayback(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, guard := range []string{"busy", "more", "save", "info", "tiny"} {
			for _, waveform := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/%s/waveform_%t", size[0], size[1], guard, waveform), func(t *testing.T) {
					m := audioResultNavigationModel(t, size[0], size[1])
					require.Nil(t, audioReviewKey(m, tea.KeyRight))
					if waveform {
						require.Nil(t, audioReviewKey(m, tea.KeyUp))
					}
					playing := audioResultNavigationPlayback(t, m)
					switch guard {
					case "busy":
						m.busy = true
					case "save":
						m.Update(tea.KeyPressMsg{Code: 's'})
					case "info":
						m.openInformation("Help", "Synthetic help", "")
					case "tiny":
						m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
					default:
						m.modal = guard
					}
					for _, key := range []tea.KeyPressMsg{
						{Code: tea.KeyLeft}, {Code: tea.KeyRight},
						{Code: tea.KeyLeft, Mod: tea.ModShift}, {Code: tea.KeyRight, Mod: tea.ModShift},
						{Code: tea.KeyUp}, {Code: tea.KeyDown}, {Code: tea.KeyHome}, {Code: tea.KeyEnd},
					} {
						m.Update(key)
						require.Equal(t, 4, m.focus)
						require.Equal(t, 1, m.resultAction)
						require.Equal(t, waveform, m.waveformFocused)
						require.Equal(t, 3*time.Second, m.result.position)
						require.True(t, m.playing)
						require.NoError(t, playing.Err())
						require.Equal(t, uint64(42), m.playID)
						require.Equal(t, guard == "busy", m.busy)
					}
				})
			}
		}
	}
}

func TestAudioResultNavigationSkipsUnavailableWaveform(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, unavailable := range []string{"unmeasured", "zero-duration"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], unavailable), func(t *testing.T) {
				m := audioResultNavigationModel(t, size[0], size[1])
				playing := audioResultNavigationPlayback(t, m)
				if unavailable == "unmeasured" {
					m.result.measured = false
				} else {
					m.result.analysis.Duration = 0
				}
				require.Nil(t, audioReviewKey(m, tea.KeyRight))
				require.Nil(t, audioReviewKey(m, tea.KeyUp))
				require.Equal(t, 2, m.focus)
				require.Equal(t, audioSettingMore, m.setting)
				require.False(t, m.waveformFocused)
				require.Nil(t, audioReviewKey(m, tea.KeyDown))
				require.Equal(t, 4, m.focus)
				require.Zero(t, m.resultAction)
				require.False(t, m.waveformFocused)
				require.Equal(t, 3*time.Second, m.result.position)
				require.True(t, m.playing)
				require.NoError(t, playing.Err())
			})
		}
	}
}

func TestAudioResultNavigationSaveEscapePreservesTarget(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := audioResultNavigationModel(t, size[0], size[1])
			playing := audioResultNavigationPlayback(t, m)
			result := m.result
			require.Nil(t, audioReviewKey(m, tea.KeyRight))
			require.Nil(t, audioReviewKey(m, tea.KeyUp))
			require.Nil(t, audioReviewKey(m, tea.KeyDown))
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, "save", m.modal)
			require.Equal(t, 2, m.saveFocus)
			require.False(t, m.busy, "opening Save scheduled an export")
			require.Nil(t, audioReviewKey(m, tea.KeyEscape))
			require.Empty(t, m.modal)
			require.Equal(t, 4, m.focus)
			require.Equal(t, 1, m.resultAction)
			require.False(t, m.waveformFocused)
			require.Same(t, result, m.result)
			require.Equal(t, 3*time.Second, m.result.position)
			require.True(t, m.playing)
			require.NoError(t, playing.Err())
			require.Equal(t, uint64(42), m.playID)
		})
	}
}

func TestAudioResultNavigationReplacementResetsToPlay(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, kind := range []string{"generate", "open"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], kind), func(t *testing.T) {
				m := audioResultNavigationModel(t, size[0], size[1])
				require.Nil(t, audioReviewKey(m, tea.KeyRight))
				require.Nil(t, audioReviewKey(m, tea.KeyUp))
				result := audioReviewResult("replacement-"+kind, false)
				m.Update(audioWorkMessage{kind: kind, result: result})
				require.Equal(t, result.entry.ID, m.result.entry.ID)
				require.Equal(t, 4, m.focus)
				require.Zero(t, m.resultAction)
				require.False(t, m.waveformFocused)
				require.Zero(t, m.result.position)
				require.Nil(t, audioReviewKey(m, tea.KeyEnter), "queued Enter started replacement playback")
				require.False(t, m.playing)
				require.False(t, m.busy)
			})
		}
	}
}

func TestAudioResultNavigationNoColorMarksFocusedWaveform(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			base := audioReviewModel(t)
			m := newAudioWorkbench(t.Context(), base.command, base.next, base.store)
			require.False(t, m.color)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.Update(audioWorkMessage{kind: "generate", result: audioReviewResult("no-color", false)})
			require.Contains(t, m.View().Content, "› Play audio")
			require.Nil(t, audioReviewKey(m, tea.KeyRight))
			require.Contains(t, m.View().Content, "› Save audio…")
			require.Nil(t, audioReviewKey(m, tea.KeyUp))
			view := m.View().Content
			require.NotContains(t, view, "\x1b")
			require.Contains(t, view, "←→ seek")
			require.Contains(t, view, "↓ actions")
			require.NotContains(t, view, "› Play audio")
			require.NotContains(t, view, "› Save audio")
			var focused []string
			for _, row := range strings.Split(view, "\n") {
				if strings.HasPrefix(strings.TrimSpace(row), "› ") {
					focused = append(focused, row)
				}
			}
			require.Len(t, focused, 1, "waveform focus needs one visible marker without color")
			require.Contains(t, focused[0], "┃", "the focus marker must appear beside the waveform")
			require.Nil(t, audioReviewKey(m, tea.KeyDown))
			require.Contains(t, m.View().Content, "› Save audio…")
		})
	}
}
