package audio

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
		view := audioFeedbackView(t, m)
		require.Contains(t, view, "Speech request failed.")
		require.Contains(t, view, "Check History and API usage before generating again.")
		require.NotContains(t, view, "Retained audio remains available")
		require.NotContains(t, view, "synthetic request failure")
		require.False(t, m.busy)
	}
}

func TestAudioWorkbenchPartialHistoryFailureIsVisibleWithUsableEntries(t *testing.T) {
	for _, width := range []int{40, 90} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		entry := audioReviewResult("retained", false).entry
		failure := errors.Join(audiohistory.ErrInvalid, errors.New("synthetic corrupt neighbor"))
		m.Update(audioWorkMessage{kind: "history", entries: []audiohistory.Entry{entry}, err: failure})
		require.Equal(t, "recovery", m.modal)
		require.Equal(t, "history", m.recoveryParent)
		require.Equal(t, []audiohistory.Entry{entry}, m.entries)
		require.ErrorIs(t, m.lastErr, failure)
		view := audioFeedbackView(t, m)
		require.Contains(t, view, "History could not be verified.")
		require.Contains(t, view, "Check private cache permissions and retained records.")
		require.Contains(t, view, "Error details")
		require.Nil(t, audioReviewKey(m, tea.KeyEscape))
		require.Equal(t, "history", m.modal)
		require.Contains(t, audioFeedbackView(t, m), "Enter select · Esc back")
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
					note, failure, feedback := m.note, m.lastErr, m.failure
					_, command := m.Update(audioWorkMessage{kind: "play", playID: id - 1, err: errors.New("stale failure")})
					require.Nil(t, command)
					require.True(t, m.playing)
					require.Equal(t, note, m.note)
					require.Equal(t, failure, m.lastErr)
					require.Equal(t, feedback, m.failure, "stale playback replaced newer feedback")
					_, command = m.Update(audioWorkMessage{kind: "play", playID: id, err: completion})
					require.Nil(t, command)
					require.False(t, m.playing)
					require.Equal(t, failure, m.lastErr, "playback completion cleared an unrelated failure")
					require.Equal(t, feedback, m.failure, "playback completion replaced newer feedback")
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
					view := audioFeedbackView(t, m)
					switch newer {
					case "save failure":
						require.Contains(t, view, "Save failed.")
						require.Contains(t, view, "Check the destination before saving again.")
					case "history failure":
						require.Contains(t, view, "History failed.")
						require.Contains(t, view, "Check retained records and private cache permissions.")
					case "saved":
						require.Contains(t, view, "Saved: saved.wav")
					default:
						require.Contains(t, view, "Play audio")
						require.False(t, m.activeFailure(), "an earlier error replaced completed playback feedback")
					}
				})
			}
		}
	}
}

func TestAudioWorkbenchPlaybackContextsReleasePerOperation(t *testing.T) {
	m := audioReviewModel(t)
	session, cancelSession := context.WithCancel(t.Context())
	defer cancelSession()
	m.ctx = session
	result := audioReviewResult("retained", false)
	m.result = &result
	for _, outcome := range []error{nil, errors.New("synthetic player failure"), context.Canceled, nil} {
		require.NotNil(t, m.togglePlayback())
		cancel := m.playCancel
		calls := 0
		m.playCancel = func() { calls++; cancel() }
		id := m.playID
		m.Update(audioWorkMessage{kind: "play", playID: id - 1})
		require.Zero(t, calls, "stale completion canceled the active playback")
		m.Update(audioWorkMessage{kind: "play", playID: id, err: outcome})
		require.Equal(t, 1, calls, "completed playback retained its context")
		require.Nil(t, m.playCancel)
		require.NoError(t, session.Err(), "playback completion canceled the session")
	}
	require.NotNil(t, m.togglePlayback())
	cancel := m.playCancel
	calls := 0
	m.playCancel = func() { calls++; cancel() }
	m.stopPlayback()
	m.stopPlayback()
	require.Equal(t, 1, calls)
	require.Nil(t, m.playCancel)
	require.NoError(t, session.Err())
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
						require.Equal(t, "recovery", m.modal)
						require.Nil(t, audioReviewKey(m, tea.KeyEscape))
						audioReviewKey(m, tea.KeyTab)
						require.Equal(t, 4, m.focus)
						m.lastFinished = time.Time{}
					}
					note, feedback := m.note, m.failure
					require.Nil(t, audioReviewKey(m, key))
					require.ErrorIs(t, playContext.Err(), context.Canceled)
					require.False(t, m.playing)
					require.ErrorIs(t, m.lastErr, failure)
					require.Equal(t, feedback, m.failure)
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
					require.Equal(t, feedback, m.failure, "late playback replaced newer feedback")
					if newer {
						require.True(t, m.activeFailure())
						require.Nil(t, audioReviewMore(t, m, "Last error"))
						view := audioFeedbackView(t, m)
						require.Contains(t, view, "Save failed.")
						require.Contains(t, view, "Check the destination before saving again.")
					} else {
						require.False(t, m.activeFailure())
						require.NotContains(t, audioFeedbackView(t, m), "Save failed.")
					}
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
				require.Equal(t, "recovery", m.modal)
				view := audioFeedbackView(t, m)
				require.Contains(t, view, "Playback failed.")
				require.Contains(t, view, "Check History, the player, and audio output.")
				require.NotContains(t, view, "Retained audio remains available.")
				require.NotContains(t, view, "synthetic player failure")
				require.NotContains(t, m.View().Content, "\x1b[2J")
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				require.Equal(t, "choice", m.modal)
				for range len(m.choices) {
					view := audioFeedbackView(t, m)
					require.Contains(t, view, "Enter select · Esc back")
					require.Contains(t, view, "› "+m.choices[m.choice])
					require.NotContains(t, m.View().Content, "\x1b[2J")
					require.Nil(t, audioReviewKey(m, tea.KeyDown))
					require.False(t, m.busy, "choosing settings generated speech")
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
				summary, action := "Save failed.", "Check the destination before saving again."
				if kind == "export" {
					audioReviewKey(m, 's')
					m.path.SetValue("synthetic.wav")
					require.NotNil(t, audioReviewKey(m, tea.KeyEnter))
					m.Update(audioWorkMessage{kind: "export", err: failure})
					require.Same(t, &result, m.result)
				} else {
					result.measured = false
					m.Update(audioWorkMessage{kind: "generate", result: result, err: classifiedAudioFailure(audioMeasurementFailure, "Audio is retained, but measurement failed. Compressed formats need FFmpeg on PATH.", failure)})
					summary, action = "Measurement failed.", "Retained audio remains available. Export the file for inspection."
				}
				view := audioFeedbackView(t, m)
				require.Contains(t, view, summary)
				require.Contains(t, view, action)
				require.Equal(t, "recovery", m.modal)
				require.Contains(t, view, "Error details")
				require.Equal(t, result.entry, m.result.entry)
				require.Nil(t, audioReviewKey(m, tea.KeyF1))
				require.Equal(t, "error", m.modal)
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				require.Equal(t, "recovery", m.modal)
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				require.Empty(t, m.modal)
				require.Equal(t, 2, m.focus)
				require.Equal(t, 5, m.setting)
				// A choice dialog still returns to the form before result focus.
				require.Nil(t, audioReviewKey(m, tea.KeyHome))
				require.Nil(t, audioReviewKey(m, tea.KeyEnter))
				view = audioFeedbackView(t, m)
				require.Contains(t, view, "Enter select · Esc back")
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				require.Empty(t, m.modal)
				audioReviewKey(m, tea.KeyTab)
				require.Equal(t, 4, m.focus)
				require.Nil(t, audioReviewKey(m, tea.KeyEscape))
				require.Equal(t, 0, m.focus)
				require.False(t, m.quit)
				require.True(t, m.activeFailure())
				require.Equal(t, summary, m.failure.Summary)
				require.Contains(t, m.failure.Action, action)
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
					_, command := m.Update(audioWorkMessage{kind: "generate", result: audition, err: classifiedAudioFailure(audioMeasurementFailure, "Audio is retained, but measurement failed. Compressed formats need FFmpeg on PATH.", failure)})
					require.Nil(t, command, "failed measurement automatically started playback")
					require.Equal(t, audition, *m.result)
					require.Equal(t, 4, m.focus)
					require.False(t, m.busy)
					require.False(t, m.playing)
					require.ErrorIs(t, m.lastErr, failure)
					view := audioFeedbackView(t, m)
					require.Contains(t, view, "Measurement failed.")
					require.Contains(t, view, "Retained audio remains available. Export the file for inspection.")
					require.Equal(t, "recovery", m.modal)
					if hasPrior {
						require.Same(t, &prior, m.priorResult)
					} else {
						require.Nil(t, m.priorResult)
						require.NotContains(t, view, "previous result")
					}
					require.Nil(t, audioReviewKey(m, tea.KeyEscape))
					require.Empty(t, m.modal)
				}
				audioReviewKey(m, tea.KeyTab)
				require.Equal(t, 4, m.focus)
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
				require.True(t, m.activeFailure())
				require.Equal(t, "Measurement failed.", m.failure.Summary)
				require.Contains(t, m.failure.Action, "Retained audio remains available.")
				require.NotNil(t, audioReviewKey(m, tea.KeyEscape))
				require.True(t, m.quit)
				require.ErrorIs(t, m.lastErr, failure)
			})
		}
	}
}
