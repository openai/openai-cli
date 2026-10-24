package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func audioSeekModel(t *testing.T) *audioWorkbench {
	t.Helper()
	m := audioReviewModel(t)
	result := audioReviewResult("selected", false)
	// One second per visible bar makes movement assertions independent of rounding.
	result.analysis.Duration = float64(audioWaveformBars(m.waveformWidth()))
	m.result = &result
	m.focusSection(4)
	m.waveformFocused = true
	t.Cleanup(m.stopPlayback)
	return m
}

func audioSeekKey(m *audioWorkbench, code rune, modified bool) tea.Cmd {
	key := tea.KeyPressMsg{Code: code}
	if modified {
		key.Mod = tea.ModShift
	}
	_, command := m.Update(key)
	return command
}

func TestAudioSeekKeysSelectBoundedPlaybackStart(t *testing.T) {
	m := audioSeekModel(t)
	finished := time.Now().Add(-time.Second)
	m.lastFinished = finished
	for _, step := range []struct {
		key      rune
		modified bool
		want     time.Duration
	}{
		{tea.KeyLeft, false, 0},
		{tea.KeyRight, false, time.Second},
		{tea.KeyRight, true, 6 * time.Second},
		{tea.KeyLeft, false, 5 * time.Second},
		{tea.KeyLeft, true, 0},
		{tea.KeyLeft, true, 0},
	} {
		require.Nil(t, audioSeekKey(m, step.key, step.modified))
		require.Equal(t, step.want, m.result.position)
		require.False(t, m.busy)
		require.False(t, m.playing)
		require.Zero(t, m.playID)
		require.Equal(t, finished, m.lastFinished, "seeking changed the repeat-submit guard")
	}
	for range 100 {
		require.Nil(t, audioSeekKey(m, tea.KeyRight, true))
	}
	require.Equal(t, m.result.seekDuration(), m.result.position)
	for _, key := range []rune{tea.KeyEnter, tea.KeySpace} {
		require.Nil(t, audioReviewKey(m, key), "the endpoint must not restart playback at zero")
		require.False(t, m.playing)
	}
	require.Nil(t, audioSeekKey(m, tea.KeyLeft, false))
	require.Equal(t, m.result.seekDuration()-time.Second, m.result.position)
	m.lastFinished = time.Now()
	require.Nil(t, audioReviewKey(m, tea.KeySpace), "seeking bypassed the repeat-submit guard")
	m.lastFinished = finished
	require.NotNil(t, audioReviewKey(m, tea.KeySpace), "explicit playback was not scheduled")
}

func TestAudioSeekUnmeasuredAndEmptyDurationsDoNotMove(t *testing.T) {
	for _, test := range []struct {
		name     string
		duration float64
		measured bool
	}{
		{"empty", 0, true},
		{"negative", -1, true},
		{"nan", math.NaN(), true},
		{"infinite", math.Inf(1), true},
		{"overflow", float64(math.MaxInt64) / float64(time.Second), true},
		{"unmeasured", 8, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := audioSeekModel(t)
			m.result.analysis.Duration, m.result.measured = test.duration, test.measured
			for _, key := range []rune{tea.KeyLeft, tea.KeyRight} {
				require.Nil(t, audioSeekKey(m, key, true))
				require.Zero(t, m.result.position)
				require.False(t, m.playing)
			}
		})
	}
	for _, duration := range []time.Duration{time.Second / 24000, 125 * time.Millisecond} {
		m := audioSeekModel(t)
		m.result.analysis.Duration = duration.Seconds()
		for range 100 {
			audioSeekKey(m, tea.KeyRight, true)
		}
		require.Equal(t, duration, m.result.position, "short audio could not reach its endpoint")
	}
}

func TestAudioSeekRespectsFocusModalBusyAndSizeGuards(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*audioWorkbench)
	}{
		{"save action", func(m *audioWorkbench) { m.resultAction = 1; m.waveformFocused = false }},
		{"text", func(m *audioWorkbench) { m.focusSection(0) }},
		{"instructions", func(m *audioWorkbench) { m.focusSection(1) }},
		{"settings", func(m *audioWorkbench) { m.focusSection(2) }},
		{"busy", func(m *audioWorkbench) { m.busy = true }},
		{"narrow", func(m *audioWorkbench) { m.Update(tea.WindowSizeMsg{Width: 39, Height: 32}) }},
		{"short", func(m *audioWorkbench) { m.Update(tea.WindowSizeMsg{Width: 100, Height: 11}) }},
		{"save modal", func(m *audioWorkbench) { m.openSave() }},
		{"speed modal", func(m *audioWorkbench) { m.modal = "speed"; m.speedInput.Focus() }},
		{"history modal", func(m *audioWorkbench) { m.modal = "history" }},
		{"choice modal", func(m *audioWorkbench) { m.modal = "choice" }},
		{"more modal", func(m *audioWorkbench) { m.modal = "more" }},
		{"information modal", func(m *audioWorkbench) { m.openInformation("Help", "Synthetic help", "") }},
		{"recovery modal", func(m *audioWorkbench) { m.rememberFailure("play", errors.New("synthetic player failure")) }},
		{"error modal", func(m *audioWorkbench) {
			m.rememberFailure("play", errors.New("synthetic player failure"))
			m.modal = "error"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := audioSeekModel(t)
			m.result.position = 3 * time.Second
			test.setup(m)
			for _, key := range []rune{tea.KeyLeft, tea.KeyRight} {
				audioSeekKey(m, key, false)
				audioSeekKey(m, key, true)
				require.Equal(t, 3*time.Second, m.result.position)
				require.Zero(t, m.playID)
			}
		})
	}
	m := audioSeekModel(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	require.Nil(t, audioSeekKey(m, tea.KeyRight, false))
	require.Positive(t, m.result.position, "the supported compact layout blocked seeking")
	selected := m.result.position
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	require.Equal(t, selected, m.result.position, "resize changed the selected time")
	audioReviewKey(m, 's')
	require.Equal(t, "save", m.modal)
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Equal(t, selected, m.result.position, "closing Save changed the selected time")
}

func TestAudioSeekStopsOnlyForAChangedSelection(t *testing.T) {
	m := audioSeekModel(t)
	playContext, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	canceled := 0
	m.playCancel = func() { canceled++; cancel() }
	m.playing, m.playID, m.note = true, 42, audioPlayingNote
	for _, key := range []rune{tea.KeyDown, tea.KeyHome, tea.KeyEnd, tea.KeyUp, tea.KeyUp, tea.KeyDown} {
		require.Nil(t, audioReviewKey(m, key))
		require.True(t, m.playing)
		require.Zero(t, canceled)
	}
	audioReviewKey(m, tea.KeyTab)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, 4, m.focus)
	require.True(t, m.playing)
	require.Zero(t, canceled)
	audioReviewKey(m, tea.KeyUp)
	require.True(t, m.waveformFocused)
	// At the beginning, Left cannot change the selection or stop playback.
	require.Nil(t, audioSeekKey(m, tea.KeyLeft, false))
	require.True(t, m.playing)
	require.NoError(t, playContext.Err())
	require.Nil(t, audioSeekKey(m, tea.KeyRight, false))
	require.False(t, m.playing)
	require.Equal(t, 1, canceled)
	require.ErrorIs(t, playContext.Err(), context.Canceled)
	require.Equal(t, time.Second, m.result.position)
	require.Nil(t, audioSeekKey(m, tea.KeyRight, false))
	require.Equal(t, 1, canceled)
	require.Equal(t, 2*time.Second, m.result.position)
	selected, note, id := m.result.position, m.note, m.playID
	_, command := m.Update(audioWorkMessage{kind: "play", playID: 42, err: errors.New("stale player failure")})
	require.Nil(t, command)
	require.Equal(t, selected, m.result.position)
	require.Equal(t, note, m.note)
	require.Equal(t, id, m.playID)
	require.NoError(t, m.lastErr)
}

func TestAudioSeekPreservesNewerFailureFeedback(t *testing.T) {
	m := audioSeekModel(t)
	m.playing, m.note = true, "Synthetic export failure"
	m.lastErr = errors.New("synthetic export failure")
	m.rememberFailure("export", m.lastErr)
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	m.focusSection(4)
	audioReviewKey(m, tea.KeyUp)
	note, failure, lastErr := m.note, m.failure, m.lastErr
	require.Nil(t, audioSeekKey(m, tea.KeyRight, false))
	require.False(t, m.playing)
	require.Equal(t, time.Second, m.result.position)
	require.Equal(t, note, m.note)
	require.Equal(t, failure, m.failure)
	require.ErrorIs(t, m.lastErr, lastErr)
	require.True(t, m.activeFailure())
}

func TestAudioSeekResultReplacementAndPreviewRestoration(t *testing.T) {
	m := audioSeekModel(t)
	require.Nil(t, audioSeekKey(m, tea.KeyRight, true))
	original := m.result
	for _, id := range []string{"preview one", "preview two"} {
		m.Update(audioWorkMessage{kind: "generate", result: audioReviewResult(id, true)})
		require.Zero(t, m.result.position)
		require.Same(t, original, m.priorResult)
	}
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Same(t, original, m.result)
	require.Equal(t, 5*time.Second, m.result.position)
	for _, kind := range []string{"generate", "open"} {
		m.Update(audioWorkMessage{kind: kind, err: errors.New("synthetic replacement failure")})
		require.Same(t, original, m.result)
		require.Equal(t, 5*time.Second, m.result.position)
	}
	for _, kind := range []string{"generate", "open"} {
		m.Update(audioWorkMessage{kind: kind, result: audioReviewResult(kind, false)})
		require.Zero(t, m.result.position)
		require.Nil(t, m.priorResult)
		m.result.position = 500 * time.Millisecond
	}
}

func audioSeekWorker(t *testing.T, command tea.Cmd) audioWorkMessage {
	t.Helper()
	require.NotNil(t, command)
	batch, ok := command().(tea.BatchMsg)
	require.True(t, ok)
	require.NotEmpty(t, batch)
	message, ok := batch[0]().(audioWorkMessage)
	require.True(t, ok)
	return message
}

func TestAudioSeekSaveExportsFullOriginal(t *testing.T) {
	m := audioSeekModel(t)
	settings := audioDefaultSettings()
	settings.Format = "pcm"
	original := bytes.Repeat([]byte{17, 23, 42, 91}, 24000)
	entry, err := m.store.Capture(t.Context(), io.NopCloser(bytes.NewReader(original)), settings)
	require.NoError(t, err)
	message := m.load(t.Context(), entry.ID, "open")
	require.NoError(t, message.err)
	m.Update(message)
	audioReviewKey(m, tea.KeyUp)
	require.Nil(t, audioSeekKey(m, tea.KeyRight, true))
	require.Positive(t, m.result.position)
	position := m.result.position
	audioReviewKey(m, 's')
	require.Equal(t, "save", m.modal)
	path := filepath.Join(t.TempDir(), "original.pcm")
	m.path.SetValue(path)
	saved := audioSeekWorker(t, audioReviewKey(m, tea.KeyEnter))
	require.NoError(t, saved.err)
	m.Update(saved)
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, bytes.Equal(original, actual), "Save omitted or changed original audio bytes")
	require.Equal(t, position, m.result.position)
}

// The wrapper launches this subprocess instead of an audio device. Its input
// comes through the production worker, retained snapshot, and media player.
func TestAudioSeekPlayerProcess(t *testing.T) {
	index := slices.Index(os.Args, "--audio-seek-player")
	if index < 0 {
		return
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil || index+1 >= len(os.Args) {
		os.Exit(7)
	}
	if err := os.WriteFile(os.Args[index+1], input, 0600); err != nil {
		os.Exit(8)
	}
	os.Exit(0)
}

func TestAudioSeekWorkerCapturesOffsetAndPreservesOriginal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake player uses a Unix shell wrapper; native Windows playback needs separate evidence")
	}
	binaryPath, err := os.Executable()
	require.NoError(t, err)
	for _, format := range []string{"pcm", "wav"} {
		t.Run(format, func(t *testing.T) {
			m := audioSeekModel(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			m.ctx = ctx
			directory := t.TempDir()
			captured := filepath.Join(directory, "player-input")
			script := "#!/bin/sh\nexec " + audioShellQuote(binaryPath) +
				" -test.run='^TestAudioSeekPlayerProcess$' -- --audio-seek-player " + audioShellQuote(captured) + " \"$@\"\n"
			require.NoError(t, os.WriteFile(filepath.Join(directory, "ffplay"), []byte(script), 0700))
			t.Setenv("PATH", directory)
			rate, channels := 24000, 1
			if format == "wav" {
				rate, channels = 8000, 2
			}
			// Two visible bars select exactly 500ms in both encodings.
			frames := audioWaveformBars(m.waveformWidth()) * rate / 4
			pcm := make([]byte, frames*channels*2)
			for index := range pcm {
				pcm[index] = byte(index % 251)
			}
			original := pcm
			if format == "wav" {
				original = audioSeekWave(pcm, rate, channels)
			}
			settings := audioDefaultSettings()
			settings.Format = format
			entry, err := m.store.Capture(ctx, io.NopCloser(bytes.NewReader(original)), settings)
			require.NoError(t, err)
			loaded := m.load(ctx, entry.ID, "open")
			require.NoError(t, loaded.err)
			m.Update(loaded)
			audioReviewKey(m, tea.KeyUp)
			for range 2 {
				require.Nil(t, audioSeekKey(m, tea.KeyRight, false))
			}
			require.Equal(t, 500*time.Millisecond, m.result.position)
			m.lastFinished = time.Now().Add(-time.Second)
			command := audioReviewKey(m, tea.KeyEnter)
			require.NotNil(t, command)
			// A queued worker must own its offset, not read later mutable selection.
			m.result.position = 250 * time.Millisecond
			played := audioSeekWorker(t, command)
			require.NoError(t, played.err)
			m.Update(played)
			require.False(t, m.playing)
			require.Equal(t, 250*time.Millisecond, m.result.position)
			input, err := os.ReadFile(captured)
			require.NoError(t, err)
			want := pcm[rate/2*channels*2:]
			if format == "wav" {
				want = audioSeekWave(want, rate, channels)
			}
			require.True(t, bytes.Equal(want, input), "the player did not receive the selected sample range")
			snapshot, err := m.store.Open(ctx, entry.ID)
			require.NoError(t, err)
			retained, readErr := io.ReadAll(snapshot.File)
			closeErr := snapshot.Close()
			require.NoError(t, readErr)
			require.NoError(t, closeErr)
			require.True(t, bytes.Equal(original, retained), "playback changed the retained original")
		})
	}
}

func audioSeekWave(pcm []byte, rate, channels int) []byte {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(pcm)))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(header[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(pcm)))
	return append(header, pcm...)
}
