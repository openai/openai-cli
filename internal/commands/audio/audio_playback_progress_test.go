package audio

import (
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

func TestAudioPlaybackProgressCompletionClampsPosition(t *testing.T) {
	m := audioSeekModel(t)
	p := &audioPlaybackProgress{entryID: m.result.entry.ID}
	m.playing, m.playProgress = true, p
	p.record(m.result.seekDuration() + time.Second)
	m.Update(audioWorkMessage{kind: "play", playID: m.playID})
	require.Equal(t, m.result.seekDuration(), m.result.position)
	require.False(t, m.playing)
	require.Nil(t, m.playProgress)
	require.True(t, m.result.finished)
	m.lastFinished = time.Now().Add(-time.Second)
	require.NotNil(t, audioReviewKey(m, tea.KeySpace), "completed audio must replay")
	require.Zero(t, m.result.position)
	require.False(t, m.result.finished)
}
