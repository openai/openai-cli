package audio

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestAudioPlaybackProgressUsesPlayerClockAndFreezesOnStop(t *testing.T) {
	m := audioSeekModel(t)
	m.result.position = time.Second
	p := &audioPlaybackProgress{entryID: m.result.entry.ID}
	m.playing, m.playProgress = true, p
	m.Update(audioWorkTick(time.Now().Add(time.Hour)))
	require.Equal(t, time.Second, m.result.position, "ticks cannot invent playback progress")
	p.record(2500 * time.Millisecond)
	m.Update(audioWorkTick(time.Now()))
	require.Equal(t, 2500*time.Millisecond, m.result.position)
	p.record(time.Second)
	m.Update(audioWorkTick(time.Now()))
	require.Equal(t, 2500*time.Millisecond, m.result.position, "regressing reports must not rewind")
	p.record(3 * time.Second)
	m.stopPlayback()
	require.Equal(t, 3*time.Second, m.result.position, "stop captures the last player report")
	p.record(4 * time.Second)
	m.Update(audioWorkTick(time.Now()))
	require.Equal(t, 3*time.Second, m.result.position, "stopped callbacks cannot move the marker")
}

func TestAudioPlaybackProgressRejectsReplacedPlayersAndEntries(t *testing.T) {
	m := audioSeekModel(t)
	old := &audioPlaybackProgress{entryID: m.result.entry.ID}
	current := &audioPlaybackProgress{entryID: m.result.entry.ID}
	m.playing, m.playProgress, m.playID = true, current, 9
	old.record(10 * time.Second)
	current.record(2 * time.Second)
	m.Update(audioWorkMessage{kind: "play", playID: 8})
	require.True(t, m.playing)
	m.Update(audioWorkTick(time.Now()))
	require.Equal(t, 2*time.Second, m.result.position)
	m.result.entry.ID = "replacement"
	current.record(3 * time.Second)
	m.Update(audioWorkTick(time.Now()))
	require.Equal(t, 2*time.Second, m.result.position)
}

func TestAudioPlaybackOffersPauseOnlyWithMeasuredPlayerProgress(t *testing.T) {
	m := audioSeekModel(t)
	m.playing, m.playProgress = true, &audioPlaybackProgress{entryID: m.result.entry.ID}
	require.Equal(t, "Stop", m.playbackVerb(), "a player without a clock cannot offer Pause")
	m.result.measured = false
	m.playProgress.record(time.Second)
	m.Update(audioWorkTick(time.Now()))
	require.Equal(t, "Stop", m.playbackVerb(), "a clock alone cannot make an unmeasured recording resumable")
	m.result.measured = true
	m.Update(audioWorkTick(time.Now()))
	require.Equal(t, "Pause", m.playbackVerb())
	audioReviewKey(m, tea.KeySpace)
	require.Equal(t, "Resume", m.playbackVerb())
	m.seekResult("right")
	require.Equal(t, "Play", m.playbackVerb(), "an explicit seek replaces the pause position")
}

func TestAudioPlaybackProgressCompletionClampsPosition(t *testing.T) {
	for _, report := range []string{"short-final-sample", "no-clock-reports", "overshoot"} {
		t.Run(report, func(t *testing.T) {
			m := audioSeekModel(t)
			m.result.position = time.Second
			// Establish the public playback state, but never execute the media command.
			require.NotNil(t, audioReviewKey(m, tea.KeySpace))
			p := m.playProgress
			require.NotNil(t, p)
			duration := m.result.seekDuration()
			if report == "short-final-sample" {
				p.record(duration - 200*time.Millisecond)
			} else if report == "overshoot" {
				p.record(duration + time.Second)
			}
			_, command := m.Update(audioWorkMessage{kind: "play", playID: m.playID})
			require.Nil(t, command)
			require.Equal(t, duration, m.result.position)
			require.Equal(t, audioWaveformBars(m.waveformWidth()), m.selectedBoundary(), "successful completion must reach the final waveform boundary")
			require.False(t, m.playing)
			require.Nil(t, m.playProgress)
			require.Nil(t, m.playCancel)
			require.True(t, m.result.finished)
			m.lastFinished = time.Now().Add(-time.Second)
			require.NotNil(t, audioReviewKey(m, tea.KeySpace), "completed audio must replay")
			require.Zero(t, m.result.position)
			require.False(t, m.result.finished)
		})
	}
}

func TestAudioPlaybackProgressIncompletePlaybackKeepsLastReport(t *testing.T) {
	for _, outcome := range []string{"stopped", "canceled", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			m := audioSeekModel(t)
			m.result.position = time.Second
			require.NotNil(t, audioReviewKey(m, tea.KeySpace))
			id, p := m.playID, m.playProgress
			const last = 2500 * time.Millisecond
			p.record(last)
			var completion error
			switch outcome {
			case "stopped":
				require.Nil(t, audioReviewKey(m, 'x'))
				p.record(3 * time.Second)
			case "canceled":
				completion = context.Canceled
			case "failed":
				completion = errors.New("synthetic playback failure")
			}
			_, command := m.Update(audioWorkMessage{kind: "play", playID: id, err: completion})
			require.Nil(t, command)
			require.Equal(t, last, m.result.position)
			require.False(t, m.result.finished)
			require.False(t, m.playing)
			require.Nil(t, m.playProgress)
			require.Nil(t, m.playCancel)
			if outcome == "failed" {
				require.ErrorIs(t, m.lastErr, completion)
			} else {
				require.NoError(t, m.lastErr)
			}
		})
	}
}

func TestAudioPlaybackProgressCompletionCannotFinishReplacedEntry(t *testing.T) {
	m := audioSeekModel(t)
	require.NotNil(t, audioReviewKey(m, tea.KeySpace))
	id, p := m.playID, m.playProgress
	p.record(2 * time.Second)
	m.Update(audioWorkTick(time.Now()))
	_, command := m.Update(audioWorkMessage{kind: "play", playID: id - 1})
	require.Nil(t, command)
	require.Equal(t, 2*time.Second, m.result.position)
	require.False(t, m.result.finished)
	require.True(t, m.playing)
	require.Same(t, p, m.playProgress)

	replacement := audioReviewResult("different-entry", false)
	replacement.position = 500 * time.Millisecond
	m.result = &replacement
	p.record(3 * time.Second)
	_, command = m.Update(audioWorkMessage{kind: "play", playID: id})
	require.Nil(t, command)
	require.Equal(t, 500*time.Millisecond, m.result.position)
	require.False(t, m.result.finished, "completion belongs to the previous recording")
	require.False(t, m.playing)
	require.Nil(t, m.playProgress)
}

func TestAudioPlaybackProgressCompletionDoesNotInventUnknownDuration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		measured bool
		duration float64
	}{
		{"unmeasured", false, 10},
		{"zero", true, 0},
		{"negative", true, -1},
		{"nan", true, math.NaN()},
		{"infinite", true, math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := audioSeekModel(t)
			m.result.measured, m.result.analysis.Duration = tc.measured, tc.duration
			require.NotNil(t, audioReviewKey(m, tea.KeySpace))
			_, command := m.Update(audioWorkMessage{kind: "play", playID: m.playID})
			require.Nil(t, command)
			require.Zero(t, m.result.position)
			require.Zero(t, m.selectedBoundary())
			require.True(t, m.result.finished)
			require.False(t, m.playing)
		})
	}
}
