package custom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
)

func TestAudioWorkbenchFailurePreservesKnownResultWithoutInventingRetention(t *testing.T) {
	prior := audioReviewResult("retained", false)
	for _, result := range []*audioWorkResult{nil, &prior} {
		m := audioReviewModel(t)
		m.result = result
		m.input.SetValue("Keep the draft text.")
		m.instructions.SetValue("Keep these instructions.")
		settings := m.settings
		failure := errors.New("synthetic request failure")
		m.Update(audioWorkMessage{kind: "generate", err: failure})
		if result == nil {
			require.Nil(t, m.result)
		} else {
			require.Same(t, result, m.result)
		}
		require.ErrorIs(t, m.lastErr, failure)
		require.Equal(t, "Keep the draft text.", m.input.Value())
		require.Equal(t, "Keep these instructions.", m.instructions.Value())
		require.Equal(t, settings, m.settings)
		require.Contains(t, m.View().Content, "Audio action failed.")
		require.NotContains(t, m.View().Content, "retained audio remains available")
		require.False(t, m.busy)
	}
}

func TestAudioWorkbenchPartialHistoryFailureIsVisibleWithUsableEntries(t *testing.T) {
	for _, width := range []int{40, 90} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		entry := audioReviewResult("retained", false).entry
		failure := errors.New("synthetic corrupt neighbor")
		m.Update(audioWorkMessage{kind: "history", entries: []audiohistory.Entry{entry}, err: failure})
		require.Equal(t, "history", m.modal)
		require.Equal(t, []audiohistory.Entry{entry}, m.entries)
		require.ErrorIs(t, m.lastErr, failure)
		require.Contains(t, m.View().Content, "Some audio history could not be read.")
		_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.NotNil(t, command, "valid history selection was disabled by its corrupt neighbor")
	}
}

func audioFeedbackView(t *testing.T, m *audioWorkbench) string {
	t.Helper()
	view := ansi.Strip(m.View().Content)
	rows := strings.Split(view, "\n")
	require.LessOrEqual(t, len(rows), m.height-1)
	for _, row := range rows {
		require.LessOrEqual(t, ansi.StringWidth(row), m.width-1)
	}
	return strings.Join(strings.Fields(view), " ")
}

func TestAudioWorkbenchPlaybackCompletionOwnsOnlyItsActiveNote(t *testing.T) {
	for _, width := range []int{40, 90} {
		for _, newer := range []string{"none", "saved", "save failure", "history failure"} {
			for _, completion := range []error{nil, context.Canceled} {
				t.Run(fmt.Sprintf("%d/%s/canceled_%t", width, newer, completion != nil), func(t *testing.T) {
					m := audioReviewModel(t)
					m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
					result := audioReviewResult("retained", false)
					m.result, m.focus = &result, 4
					m.lastErr = errors.New("earlier action failed")
					require.NotNil(t, audioReviewKey(m, tea.KeySpace))
					require.True(t, m.playing)
					require.Equal(t, "Playing. Space or X stops.", m.note)
					id := m.playID
					switch newer {
					case "saved":
						m.Update(audioWorkMessage{kind: "export", entryID: result.entry.ID, path: "saved.wav"})
					case "save failure":
						m.Update(audioWorkMessage{kind: "export", err: errors.New("synthetic export failure")})
					case "history failure":
						m.Update(audioWorkMessage{kind: "history", entries: []audiohistory.Entry{result.entry}, err: errors.New("synthetic history failure")})
					}
					note, failure := m.note, m.lastErr
					_, command := m.Update(audioWorkMessage{kind: "play", playID: id - 1, err: errors.New("stale failure")})
					require.Nil(t, command)
					require.True(t, m.playing)
					require.Equal(t, note, m.note)
					require.Equal(t, failure, m.lastErr)
					_, command = m.Update(audioWorkMessage{kind: "play", playID: id, err: completion})
					require.Nil(t, command)
					require.False(t, m.playing)
					require.Equal(t, failure, m.lastErr, "playback completion cleared an unrelated failure")
					require.Same(t, &result, m.result)
					if newer == "none" {
						expected := "Finished."
						if completion != nil {
							expected = "Stopped."
						}
						require.Equal(t, expected, m.note)
					} else {
						require.Equal(t, note, m.note)
					}
					require.Contains(t, audioFeedbackView(t, m), m.note)
				})
			}
		}
	}
}

func TestAudioWorkbenchStopKeysPreserveNewerFeedbackAndErrors(t *testing.T) {
	for _, width := range []int{40, 90} {
		for _, key := range []rune{'x', tea.KeyEscape, tea.KeySpace} {
			for _, newer := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/key_%d/newer_%t", width, key, newer), func(t *testing.T) {
					m := audioReviewModel(t)
					m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
					result := audioReviewResult("retained", false)
					m.result, m.focus = &result, 4
					failure := errors.New("synthetic export failure")
					m.lastErr = failure
					require.NotNil(t, audioReviewKey(m, tea.KeySpace))
					playContext, cancel := context.WithCancel(t.Context())
					t.Cleanup(cancel)
					m.playCancel = cancel
					id := m.playID
					if newer {
						m.Update(audioWorkMessage{kind: "export", err: failure})
						m.lastFinished = time.Time{}
					}
					note := m.note
					require.Nil(t, audioReviewKey(m, key))
					require.ErrorIs(t, playContext.Err(), context.Canceled)
					require.False(t, m.playing)
					require.ErrorIs(t, m.lastErr, failure)
					require.Same(t, &result, m.result)
					if newer {
						require.Equal(t, note, m.note)
					} else {
						require.Equal(t, "Stopped.", m.note)
					}
					if key == tea.KeyEscape {
						require.Equal(t, 0, m.focus)
						require.False(t, m.quit)
					}
					m.Update(audioWorkMessage{kind: "play", playID: id, err: errors.New("late canceled worker")})
					require.ErrorIs(t, m.lastErr, failure)
					require.Contains(t, audioFeedbackView(t, m), m.note)
				})
			}
		}
	}
}

func TestAudioWorkbenchChoiceDialogsShowAsynchronousPlaybackFailure(t *testing.T) {
	for _, width := range []int{40, 90} {
		for setting := range 3 {
			t.Run(fmt.Sprintf("%d/setting_%d", width, setting), func(t *testing.T) {
				m := audioReviewModel(t)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
				result := audioReviewResult("retained", false)
				m.result, m.focus = &result, 4
				m.input.SetValue("Keep the draft.")
				m.instructions.SetValue("Keep the instructions.")
				require.NotNil(t, audioReviewKey(m, tea.KeySpace))
				for range 3 {
					audioReviewKey(m, tea.KeyTab)
				}
				require.Equal(t, 2, m.focus)
				m.setting = setting
				require.Nil(t, audioReviewKey(m, tea.KeyEnter))
				settings := m.settings
				failure := errors.New("synthetic player failure\x1b[2J")
				_, command := m.Update(audioWorkMessage{kind: "play", playID: m.playID, err: failure})
				require.Nil(t, command)
				for range len(m.choices) {
					view := audioFeedbackView(t, m)
					require.Contains(t, view, "Playback failed.")
					require.Contains(t, view, "FFplay")
					require.Contains(t, view, "FFmpeg.")
					require.Contains(t, view, "Enter")
					require.Contains(t, view, "Choosing never generates speech.")
					require.Contains(t, view, "> "+m.choices[m.choice])
					require.NotContains(t, m.View().Content, "\x1b[2J")
					require.Nil(t, audioReviewKey(m, tea.KeyDown))
				}
				require.Equal(t, settings, m.settings)
				require.ErrorIs(t, m.lastErr, failure)
				require.Same(t, &result, m.result)
				require.Nil(t, audioReviewKey(m, tea.KeyDown))
				selected := m.choices[m.choice]
				require.Nil(t, audioReviewKey(m, tea.KeyEnter))
				require.Empty(t, m.modal)
				require.Equal(t, selected, []string{m.settings.Model, m.settings.Voice, m.settings.Format}[setting])
				require.False(t, m.busy)
				require.Equal(t, "Keep the draft.", m.input.Value())
				require.Equal(t, "Keep the instructions.", m.instructions.Value())
				require.Equal(t, result.entry.Settings, m.result.entry.Settings)
			})
		}
	}
}

func TestAudioWorkbenchFailureGuidanceFollowsEscapeTransitions(t *testing.T) {
	for _, width := range []int{40, 90} {
		for _, kind := range []string{"export", "measurement"} {
			t.Run(fmt.Sprintf("%d/%s", width, kind), func(t *testing.T) {
				m := audioReviewModel(t)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
				result := audioReviewResult("retained", false)
				m.result, m.focus = &result, 4
				m.input.SetValue("Keep the draft.")
				m.instructions.SetValue("Keep the instructions.")
				settings := m.settings
				failure := errors.New("synthetic " + kind + " failure")
				if kind == "export" {
					audioReviewKey(m, 's')
					m.path.SetValue("synthetic.wav")
					require.NotNil(t, audioReviewKey(m, tea.KeyEnter))
					m.Update(audioWorkMessage{kind: "export", err: failure})
					require.Same(t, &result, m.result)
				} else {
					result.measured = false
					m.Update(audioWorkMessage{kind: "generate", result: result, err: failure})
				}
				require.Contains(t, audioFeedbackView(t, m), "Audio action failed. Esc returns to Text.")
				require.Equal(t, result.entry, m.result.entry)
				// A choice dialog adds its own return transition before result focus.
				for range 3 {
					audioReviewKey(m, tea.KeyTab)
				}
				require.Nil(t, audioReviewKey(m, tea.KeyEnter))
				require.Contains(t, audioFeedbackView(t, m), "Audio action failed. Esc returns.")
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				require.Empty(t, m.modal)
				for range 2 {
					audioReviewKey(m, tea.KeyTab)
				}
				require.Equal(t, 4, m.focus)
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				require.Equal(t, 0, m.focus)
				require.False(t, m.quit)
				require.Contains(t, audioFeedbackView(t, m), "Audio action failed. Esc exits with the error.")
				require.ErrorIs(t, m.lastErr, failure)
				require.Equal(t, "Keep the draft.", m.input.Value())
				require.Equal(t, "Keep the instructions.", m.instructions.Value())
				require.Equal(t, settings, m.settings)
				require.NotNil(t, audioReviewKey(m, tea.KeyEscape))
				require.True(t, m.quit)
				require.ErrorIs(t, m.lastErr, failure)
			})
		}
	}
}

func TestAudioWorkbenchFailedAuditionPreservesFailureAndKnownPriorResult(t *testing.T) {
	for _, width := range []int{40, 90} {
		for _, hasPrior := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/prior_%t", width, hasPrior), func(t *testing.T) {
				m := audioReviewModel(t)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
				prior := audioReviewResult("original", false)
				if hasPrior {
					m.result = &prior
				}
				m.input.SetValue("Keep the draft.")
				m.instructions.SetValue("Keep the instructions.")
				settings := m.settings
				failure := errors.New("synthetic audition measurement failure")
				for _, id := range []string{"first_audition", "second_audition"} {
					audition := audioReviewResult(id, true)
					audition.measured = false
					_, command := m.Update(audioWorkMessage{kind: "generate", result: audition, err: failure})
					require.Nil(t, command, "failed measurement automatically started playback")
					require.Equal(t, audition, *m.result)
					require.Equal(t, 4, m.focus)
					require.False(t, m.busy)
					require.False(t, m.playing)
					require.ErrorIs(t, m.lastErr, failure)
					view := audioFeedbackView(t, m)
					require.Contains(t, view, "Audio action failed.")
					if hasPrior {
						require.Same(t, &prior, m.priorResult)
						require.Contains(t, view, "Esc restores your previous result.")
					} else {
						require.Nil(t, m.priorResult)
						require.NotContains(t, view, "previous result")
						require.Contains(t, view, "Esc returns to Text.")
					}
				}
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				if hasPrior {
					require.Same(t, &prior, m.result)
				} else {
					require.Nil(t, m.result)
				}
				require.Nil(t, m.priorResult)
				require.Equal(t, 0, m.focus)
				require.False(t, m.quit)
				require.Equal(t, "Keep the draft.", m.input.Value())
				require.Equal(t, "Keep the instructions.", m.instructions.Value())
				require.Equal(t, settings, m.settings)
				require.Contains(t, audioFeedbackView(t, m), "Esc exits with the error.")
				require.NotNil(t, audioReviewKey(m, tea.KeyEscape))
				require.True(t, m.quit)
				require.ErrorIs(t, m.lastErr, failure)
			})
		}
	}
}
