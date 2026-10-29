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
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

func TestAudioTypedSeekExactTimeAndEndpoints(t *testing.T) {
	m := audioSeekModel(t)
	m.result.analysis.Duration = 62.5
	finished := m.lastFinished
	for _, trial := range []struct {
		text string
		want time.Duration
	}{
		{"2.718281828", 2718281828 * time.Nanosecond},
		{"1:00.125", 60*time.Second + 125*time.Millisecond},
		{"0", 0},
		{"1:02.500", m.result.seekDuration()},
	} {
		audioReviewKey(m, 'g')
		require.Equal(t, "seek", m.modal)
		m.Update(tea.PasteMsg{Content: trial.text})
		require.Nil(t, audioReviewKey(m, tea.KeyEnter))
		require.Equal(t, trial.want, m.result.position)
		require.Equal(t, finished, m.lastFinished)
		require.Empty(t, m.modal)
		require.False(t, m.busy)
		require.False(t, m.playing)
		require.Zero(t, m.playID)
	}
	require.Nil(t, audioReviewKey(m, tea.KeySpace), "selecting the endpoint must not replay the recording")
	m.result.finished = true
	audioReviewKey(m, 'g')
	m.Update(tea.PasteMsg{Content: "62.500"})
	audioReviewKey(m, tea.KeyEnter)
	require.False(t, m.result.finished)
	require.Nil(t, audioReviewKey(m, tea.KeySpace), "an explicit endpoint also replaces completed playback")
}

func TestAudioTypedSeekRestoresInvalidInputAndAcceptsRetry(t *testing.T) {
	m := audioSeekModel(t)
	m.result.analysis.Duration = 75.125
	position := 2345678901 * time.Nanosecond
	for _, input := range []string{"", "invalid", "-0.1", "1:60", "1e1", "1:00:10", "2.0000000001", "1:15.126", "9223372036854775808"} {
		t.Run(input, func(t *testing.T) {
			m.result.position, m.result.resumable = position, true
			audioReviewKey(m, 'g')
			m.Update(tea.PasteMsg{Content: input})
			require.Nil(t, audioReviewKey(m, tea.KeyEnter))
			require.Equal(t, "seek", m.modal)
			require.Equal(t, position, m.result.position)
			require.True(t, m.result.resumable)
			require.Zero(t, m.playID)
			require.Equal(t, "00:02.345678901", m.seekInput.Value())
			require.True(t, m.seekInput.HasSelection())
			require.NotEmpty(t, m.seekError)
			require.Contains(t, m.View().Content, m.seekError)
			m.Update(tea.PasteMsg{Content: "1:15.125"})
			require.Empty(t, m.seekError)
			require.Nil(t, audioReviewKey(m, tea.KeyEnter))
			require.Empty(t, m.modal)
			require.Equal(t, m.result.seekDuration(), m.result.position)
		})
	}
}

func TestAudioTypedSeekKeepsPlaybackUntilValidChange(t *testing.T) {
	m := audioSeekModel(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	m.playing, m.playID, m.playCancel = true, 13, cancel
	m.playProgress = &audioPlaybackProgress{entryID: m.result.entry.ID}
	m.playProgress.record(time.Second)
	audioReviewKey(m, 'g')
	m.Update(tea.PasteMsg{Content: "bad time"})
	m.playProgress.record(2 * time.Second)
	m.Update(audioWorkTick(time.Now()))
	require.Contains(t, m.View().Content, "Current  00:02.000")
	audioReviewKey(m, tea.KeyEnter)
	require.Equal(t, "00:02.000", m.seekInput.Value())
	m.playProgress.record(3 * time.Second)
	audioReviewKey(m, tea.KeyEnter)
	require.Empty(t, m.modal)
	require.Equal(t, 3*time.Second, m.result.position)
	audioReviewKey(m, '4')
	require.Equal(t, "4", m.seekInput.Value())
	audioReviewKey(m, tea.KeyEscape)
	require.Empty(t, m.modal)
	require.True(t, m.playing)
	require.NoError(t, ctx.Err())
	require.EqualValues(t, 13, m.playID)
	audioReviewKey(m, '3')
	audioReviewKey(m, tea.KeyEnter)
	require.True(t, m.playing, "selecting the current time leaves the player running")
	audioReviewKey(m, '4')
	audioReviewKey(m, tea.KeyEnter)
	require.False(t, m.playing)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, 4*time.Second, m.result.position)
	m.Update(audioWorkMessage{kind: "play", playID: 13, err: context.Canceled})
	require.Equal(t, 4*time.Second, m.result.position)
}

func TestAudioTypedSeekEditsTheFramedTimeline(t *testing.T) {
	m := audioReviewModel(t)
	r := audioReviewResult("seek", false)
	m.result, m.focus = &r, 4
	for _, size := range [][2]int{{60, 24}, {80, 24}, {101, 29}, {101, 30}, {137, 41}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		audioReviewKey(m, '9')
		view := m.View()
		require.Contains(t, view.Content, "Text to speech")
		require.Contains(t, view.Content, "┷")
		require.Contains(t, view.Content, "Go to time")
		require.NotNil(t, view.Cursor)
		rows := strings.Split(ansi.Strip(view.Content), "\n")
		require.Contains(t, rows[view.Cursor.Y], "Go to time  9")
		r.savedPaths = []string{"/synthetic/clip.wav"}
		require.Equal(t, view.Content, m.View().Content, "hidden receipts must not move the inline seek editor")
		r.savedPaths = nil
		audioReviewKey(m, tea.KeyEnter)
		require.Zero(t, m.result.position)
		require.Contains(t, m.View().Content, "Time exceeds the recording.")
		audioReviewKey(m, tea.KeyEscape)
	}
}

func TestAudioTypedSeekAvailabilityAndCompactView(t *testing.T) {
	m := audioSeekModel(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.resultAction, m.waveformFocused = 1, false
	require.Contains(t, m.View().Content, "G time")
	audioReviewKey(m, '7')
	require.Equal(t, "seek", m.modal)
	view := m.View()
	require.NotNil(t, view.Cursor)
	for _, text := range []string{"Go to time", "7", "Enter apply", "Esc cancel"} {
		require.Contains(t, view.Content, text)
	}
	require.NotContains(t, view.Content, "\x1b")
	m.Update(tea.PasteMsg{Content: "x"})
	audioReviewKey(m, tea.KeyEnter)
	view = m.View()
	require.Contains(t, view.Content, "Use seconds or M:SS.")
	require.NotNil(t, view.Cursor)
	require.True(t, m.seekInput.HasSelection())
	// The restored timestamp permits only underline and reset sequences in monochrome.
	underlineAndReset := regexp.MustCompile(`\x1b\[(?:4(?:;4)*|24|0)?m`)
	require.NotContains(t, underlineAndReset.ReplaceAllString(view.Content, ""), "\x1b")
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, 1, m.resultAction)
	require.False(t, m.waveformFocused)
	audioReviewKey(m, 'g')
	m.Update(audioWorkMessage{kind: "open", result: audioReviewResult("replacement", false)})
	require.Empty(t, m.modal)
	require.Zero(t, m.result.position)
	m.result.measured = false
	require.NotContains(t, m.View().Content, "G time")
	require.Nil(t, audioReviewKey(m, '7'))
	require.Empty(t, m.modal)
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
	require.Nil(t, audioReviewKey(m, tea.KeyEnter)) // Finish choosing the folder.
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
			require.Equal(t, 250*time.Millisecond, m.result.position, "the worker must not mutate the UI selection")
			m.Update(played)
			require.False(t, m.playing)
			require.Equal(t, m.result.seekDuration(), m.result.position, "successful playback reaches the measured end without clock reports")
			require.True(t, m.result.finished)
			input, err := os.ReadFile(captured)
			require.NoError(t, err)
			want := pcm[rate/2*channels*2:]
			if format == "wav" {
				want = audioSeekWave(want, rate, channels)
			}
			require.True(t, bytes.Equal(want, input), "the player did not receive the selected sample range")

			// A new clock report between the last redraw and Pause sets the resume offset.
			m.result.finished, m.result.position = false, 0
			m.playing, m.playProgress = true, &audioPlaybackProgress{entryID: entry.ID}
			m.playProgress.record(625 * time.Millisecond)
			m.Update(audioWorkTick(time.Now()))
			require.Contains(t, m.View().Content, "Pause audio")
			m.playProgress.record(750 * time.Millisecond)
			require.Nil(t, audioReviewKey(m, tea.KeySpace))
			require.Equal(t, 750*time.Millisecond, m.result.position)
			require.Contains(t, m.View().Content, "Resume audio")
			require.Contains(t, m.View().Content, "Paused")
			played = audioSeekWorker(t, audioReviewKey(m, tea.KeySpace))
			require.NoError(t, played.err)
			m.Update(played)
			input, err = os.ReadFile(captured)
			require.NoError(t, err)
			want = pcm[3*rate/4*channels*2:]
			if format == "wav" {
				want = audioSeekWave(want, rate, channels)
			}
			require.Equal(t, want, input, "Resume must send the samples beginning at the last player report")

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
