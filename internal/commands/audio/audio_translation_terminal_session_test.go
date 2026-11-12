package audio

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/stretchr/testify/require"
)

type terminalTranslationSessionFixture struct {
	events     chan terminalTranslationReceived
	aborted    chan struct{}
	abortOnce  sync.Once
	append     func(context.Context, []byte) error
	finish     func(context.Context) error
	receivers  atomic.Int32
	concurrent atomic.Bool
}

func newTerminalTranslationSessionFixture() *terminalTranslationSessionFixture {
	return &terminalTranslationSessionFixture{events: make(chan terminalTranslationReceived, 16), aborted: make(chan struct{})}
}

func (s *terminalTranslationSessionFixture) Append(ctx context.Context, data []byte) error {
	if s.append != nil {
		return s.append(ctx, data)
	}
	return ctx.Err()
}

func (s *terminalTranslationSessionFixture) Receive(ctx context.Context) (audiolive.Event, error) {
	if s.receivers.Add(1) != 1 {
		s.concurrent.Store(true)
	}
	defer s.receivers.Add(-1)
	select {
	case item := <-s.events:
		return item.event, item.err
	case <-ctx.Done():
		return audiolive.Event{}, ctx.Err()
	case <-s.aborted:
		return audiolive.Event{}, io.EOF
	}
}

func (s *terminalTranslationSessionFixture) FinishInput(ctx context.Context) error {
	if s.finish != nil {
		return s.finish(ctx)
	}
	s.events <- terminalTranslationReceived{event: audiolive.Event{Type: "session.closed"}}
	return nil
}

func (s *terminalTranslationSessionFixture) Abort() {
	s.abortOnce.Do(func() { close(s.aborted) })
}

func (s *terminalTranslationSessionFixture) emit(event audiolive.Event) {
	s.events <- terminalTranslationReceived{event: event}
}

func terminalTranslationFixtureOptions(s *terminalTranslationSessionFixture) terminalTranslationOptions {
	return terminalTranslationOptions{
		Config: audiolive.Config{Language: "en"},
		Connect: func(context.Context, audiolive.Config) (audiolive.Session, error) {
			return s, nil
		},
		Capture: func(ctx context.Context, _ string, _ func([]byte) error) error {
			<-ctx.Done()
			return ctx.Err()
		},
		DrainTimeout: time.Second,
	}
}

func terminalTranslationFixtureAudio(data []byte) audiolive.Event {
	return audiolive.Event{Type: "session.output_audio.delta", Delta: base64.StdEncoding.EncodeToString(data)}
}

func terminalTranslationTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func terminalTranslationReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestTerminalTranslationStopFlushesCaptureBeforeFinishAndDrains(t *testing.T) {
	s := newTerminalTranslationSessionFixture()
	options := terminalTranslationFixtureOptions(s)
	started, reaped := make(chan struct{}), make(chan struct{})
	var sent [][]byte
	s.append = func(ctx context.Context, data []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sent = append(sent, append([]byte(nil), data...))
		return nil
	}
	options.Capture = func(ctx context.Context, device string, appendAudio func([]byte) error) error {
		defer close(reaped)
		if device != "synthetic microphone" {
			return errors.New("wrong capture device")
		}
		if err := appendAudio([]byte{1, 2}); err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		if err := appendAudio([]byte{3, 4}); err != nil {
			return err
		}
		return ctx.Err()
	}
	options.Device, options.Listen = "synthetic microphone", true
	var played []byte
	options.Play = func(_ context.Context, reader io.ReadCloser, rate, channels int) error {
		if rate != 48000 || channels != 2 {
			return errors.New("wrong playback metadata")
		}
		var err error
		played, err = io.ReadAll(reader)
		return err
	}
	s.finish = func(context.Context) error {
		select {
		case <-reaped:
		default:
			return errors.New("capture was not reaped before FinishInput")
		}
		s.emit(audiolive.Event{Type: "session.input_transcript.delta", Delta: "Hola."})
		s.emit(audiolive.Event{Type: "session.output_transcript.delta", Delta: "Hello."})
		event := terminalTranslationFixtureAudio([]byte{0, 1, 2, 3})
		event.SampleRate, event.Channels = 48000, 2
		s.emit(event)
		s.emit(audiolive.Event{Type: "session.closed", Tokens: 17})
		return nil
	}
	stop := make(chan struct{})
	go func() { <-started; close(stop) }()
	var last terminalTranslationUpdate
	result := runTerminalTranslation(terminalTranslationTestContext(t), stop, options, func(update terminalTranslationUpdate) { last = update })
	t.Cleanup(func() { _ = result.Close() })
	require.NoError(t, result.Err)
	require.True(t, result.Complete)
	require.Equal(t, [][]byte{{1, 2}, {3, 4}}, sent)
	require.Equal(t, []byte{0, 1, 2, 3}, played)
	require.Equal(t, played, terminalTranslationReadFile(t, result.AudioPath))
	require.Equal(t, "Hola.", string(terminalTranslationReadFile(t, result.OriginalPath)))
	require.Equal(t, "Hello.", string(terminalTranslationReadFile(t, result.TranslationPath)))
	require.Equal(t, int64(4), result.AudioBytes)
	require.Equal(t, 48000, result.SampleRate)
	require.Equal(t, 2, result.Channels)
	require.Equal(t, "complete", last.State)
	require.Equal(t, int64(17), last.Tokens)
	require.False(t, s.concurrent.Load())
	require.Zero(t, s.receivers.Load())
	require.NoError(t, result.Close())
	_, err := os.Stat(result.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTerminalTranslationUsesCorrectedInputLatencyAndUsage(t *testing.T) {
	session := newTerminalTranslationSessionFixture()
	options := terminalTranslationFixtureOptions(session)
	options.Capture = func(context.Context, string, func([]byte) error) error { return nil }
	inputAt, outputAt, count, zero := 0.0, 200.0, int64(4), int64(0)
	first, second := "Hello world", "today."
	session.finish = func(context.Context) error {
		session.emit(audiolive.Event{Type: "session.input_transcript.delta", ItemID: "a", Delta: "Hel", ElapsedMS: &inputAt})
		session.emit(audiolive.Event{Type: "session.input_transcript.delta", ItemID: "a", ContentIndex: 1, Delta: "Tod"})
		session.emit(audiolive.Event{Type: "session.output_transcript.delta", Delta: "Bonjour", ElapsedMS: &outputAt})
		for _, final := range []audiolive.Event{
			{Type: "session.input_transcript.completed", ItemID: "a", Transcript: &first},
			{Type: "session.input_transcript.completed", ItemID: "a", ContentIndex: 1, Transcript: &second},
			{Type: "session.input_transcript.completed", ItemID: "a", Transcript: &first},
		} {
			session.emit(final)
		}
		session.emit(audiolive.Event{Type: "usage", InputTokens: &count, OutputTokens: &zero})
		session.emit(audiolive.Event{Type: "session.closed"})
		return nil
	}
	var last terminalTranslationUpdate
	result := runTerminalTranslation(terminalTranslationTestContext(t), make(chan struct{}), options, func(update terminalTranslationUpdate) { last = update })
	t.Cleanup(func() { require.NoError(t, result.Close()) })
	require.NoError(t, result.Err)
	require.True(t, result.Complete)
	require.Equal(t, "Hello world today.", last.Original)
	require.Equal(t, last.Original, string(terminalTranslationReadFile(t, result.OriginalPath)))
	require.True(t, last.HasLatency)
	require.Equal(t, 200*time.Millisecond, last.Latency)
	require.Equal(t, &count, last.InputTokens)
	require.Equal(t, &zero, last.OutputTokens)
}

func TestTerminalTranslationAbortReapsCaptureReceiveAndBlockedPlayback(t *testing.T) {
	s := newTerminalTranslationSessionFixture()
	options := terminalTranslationFixtureOptions(s)
	captureDone, playStarted, playDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	options.Capture = func(ctx context.Context, _ string, _ func([]byte) error) error {
		defer close(captureDone)
		<-ctx.Done()
		return ctx.Err()
	}
	options.Listen = true
	options.Play = func(_ context.Context, reader io.ReadCloser, _, _ int) error {
		defer close(playDone)
		if _, err := io.ReadFull(reader, make([]byte, 2)); err != nil {
			return err
		}
		close(playStarted)
		_, err := io.Copy(io.Discard, reader)
		return err
	}
	var finished atomic.Bool
	s.finish = func(context.Context) error { finished.Store(true); return nil }
	s.emit(terminalTranslationFixtureAudio([]byte{1, 2}))
	ctx, cancel := context.WithCancel(terminalTranslationTestContext(t))
	go func() { <-playStarted; cancel() }()
	result := runTerminalTranslation(ctx, nil, options, nil)
	t.Cleanup(func() { _ = result.Close() })
	require.ErrorIs(t, result.Err, context.Canceled)
	require.False(t, result.Complete)
	require.False(t, finished.Load())
	for _, done := range []<-chan struct{}{captureDone, playDone, s.aborted} {
		select {
		case <-done:
		default:
			t.Fatal("worker remained active after return")
		}
	}
	require.Zero(t, s.receivers.Load())
	require.Equal(t, []byte{1, 2}, terminalTranslationReadFile(t, result.AudioPath))
}

func TestTerminalTranslationAbortBeforeDrainDoesNotFinishInput(t *testing.T) {
	session := newTerminalTranslationSessionFixture()
	options := terminalTranslationFixtureOptions(session)
	options.Capture = func(context.Context, string, func([]byte) error) error { return nil }
	var finished atomic.Bool
	session.finish = func(context.Context) error { finished.Store(true); return nil }

	ctx, cancel := context.WithCancel(terminalTranslationTestContext(t))
	defer cancel()
	result := runTerminalTranslation(ctx, nil, options, func(update terminalTranslationUpdate) {
		if update.State == "stopping" {
			cancel()
		}
	})
	t.Cleanup(func() { require.NoError(t, result.Close()) })
	require.ErrorIs(t, result.Err, context.Canceled)
	require.False(t, finished.Load())
}

func TestTerminalTranslationStopBoundsBlockedAppendAndPreservesTailFailure(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "final callback failure", true: "blocked append"}[blocked], func(t *testing.T) {
			s := newTerminalTranslationSessionFixture()
			options := terminalTranslationFixtureOptions(s)
			options.DrainTimeout, options.AppendTimeout = 20*time.Millisecond, time.Hour
			failure := errors.New("private synthetic append failure")
			started, stop := make(chan struct{}), make(chan struct{})
			s.append = func(ctx context.Context, _ []byte) error {
				if blocked {
					close(started)
					<-ctx.Done()
					return ctx.Err()
				}
				return failure
			}
			options.Capture = func(ctx context.Context, _ string, appendAudio func([]byte) error) error {
				if !blocked {
					close(started)
					<-ctx.Done()
				}
				_ = appendAudio([]byte{1, 2})
				return ctx.Err() // CapturePCM prioritizes capture cancellation over callback failure.
			}
			go func() { <-started; close(stop) }()
			result := runTerminalTranslation(terminalTranslationTestContext(t), stop, options, nil)
			t.Cleanup(func() { _ = result.Close() })
			require.False(t, result.Complete)
			if blocked {
				require.ErrorIs(t, result.Err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, result.Err, failure)
			}
			require.NotContains(t, result.Err.Error(), failure.Error())
			require.Zero(t, s.receivers.Load())
		})
	}
}

func TestTerminalTranslationPartialFailuresKeepReceivedFiles(t *testing.T) {
	for _, phase := range []string{"receive", "service", "capture", "finish", "drain"} {
		t.Run(phase, func(t *testing.T) {
			s := newTerminalTranslationSessionFixture()
			options := terminalTranslationFixtureOptions(s)
			options.DrainTimeout = 30 * time.Millisecond
			failure := errors.New("private synthetic " + phase)
			stop := make(chan struct{})
			var stopOnce sync.Once
			s.emit(terminalTranslationFixtureAudio([]byte{1, 2}))
			s.emit(audiolive.Event{Type: "session.output_transcript.delta", Delta: "Partial result."})
			switch phase {
			case "receive":
				s.events <- terminalTranslationReceived{err: io.EOF}
			case "service":
				s.emit(audiolive.Event{Type: "error", Message: "An API usage limit blocked translation."})
			case "capture":
				options.Capture = func(ctx context.Context, _ string, _ func([]byte) error) error {
					<-ctx.Done()
					return failure
				}
			case "finish":
				s.finish = func(context.Context) error { return failure }
			case "drain":
				s.finish = func(context.Context) error { return nil }
			}
			result := runTerminalTranslation(terminalTranslationTestContext(t), stop, options, func(update terminalTranslationUpdate) {
				if update.Translation != "" {
					stopOnce.Do(func() { close(stop) })
				}
			})
			t.Cleanup(func() { _ = result.Close() })
			require.Error(t, result.Err)
			require.False(t, result.Complete)
			require.NotContains(t, result.Err.Error(), "private synthetic")
			require.Equal(t, []byte{1, 2}, terminalTranslationReadFile(t, result.AudioPath))
			require.Equal(t, "Partial result.", string(terminalTranslationReadFile(t, result.TranslationPath)))
			if phase == "capture" || phase == "finish" {
				require.ErrorIs(t, result.Err, failure)
			}
		})
	}
}

func TestTerminalTranslationRejectsInvalidAudioWithoutCorruptingEarlierAudio(t *testing.T) {
	for name, invalid := range map[string]audiolive.Event{
		"base64":             {Delta: "AAEA!"},
		"sample":             {Delta: "AQ=="},
		"format":             {Delta: "AQI=", Format: "mp3"},
		"rate":               {Delta: "AQI=", SampleRate: -1},
		"channels":           {Delta: "AQI=", Channels: -1},
		"alignment overflow": {Delta: "AQI=", Channels: 65536},
		"changed format":     {Delta: "AQI=", SampleRate: 48000},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTerminalTranslationSessionFixture()
			s.emit(terminalTranslationFixtureAudio([]byte{1, 2}))
			invalid.Type = "session.output_audio.delta"
			s.emit(invalid)
			result := runTerminalTranslation(terminalTranslationTestContext(t), nil, terminalTranslationFixtureOptions(s), nil)
			t.Cleanup(func() { _ = result.Close() })
			require.Error(t, result.Err)
			require.False(t, result.Complete)
			require.Equal(t, int64(2), result.AudioBytes)
			require.Equal(t, []byte{1, 2}, terminalTranslationReadFile(t, result.AudioPath))
		})
	}
}

func TestTerminalTranslationBoundedPlaybackRetainsLargeAudioAndCaptions(t *testing.T) {
	s := newTerminalTranslationSessionFixture()
	options := terminalTranslationFixtureOptions(s)
	options.Listen = true
	options.Play = func(ctx context.Context, reader io.ReadCloser, _, _ int) error {
		<-ctx.Done() // This synthetic player never consumes bytes.
		return reader.Close()
	}
	pcm := bytes.Repeat([]byte{12, 34}, 400000)
	caption := strings.Repeat("猫 speaks.\n", 2000)
	s.emit(terminalTranslationFixtureAudio(pcm))
	s.emit(audiolive.Event{Type: "session.input_transcript.delta", Delta: caption})
	s.emit(audiolive.Event{Type: "session.output_transcript.delta", Delta: caption})
	stop := make(chan struct{})
	var once sync.Once
	var last terminalTranslationUpdate
	result := runTerminalTranslation(terminalTranslationTestContext(t), stop, options, func(update terminalTranslationUpdate) {
		last = update
		if update.Translation != "" {
			once.Do(func() { close(stop) })
		}
	})
	t.Cleanup(func() { _ = result.Close() })
	require.NoError(t, result.Err)
	require.True(t, result.Complete)
	require.Contains(t, result.Warning, "could not keep up")
	require.Equal(t, pcm, terminalTranslationReadFile(t, result.AudioPath))
	require.Equal(t, caption, string(terminalTranslationReadFile(t, result.OriginalPath)))
	require.Equal(t, caption, string(terminalTranslationReadFile(t, result.TranslationPath)))
	require.LessOrEqual(t, len(last.Original), 4099)
	require.LessOrEqual(t, len(last.Translation), 4099)
	require.True(t, utf8.ValidString(last.Translation))
	require.True(t, strings.HasPrefix(last.Translation, "…"))
}

func TestTerminalTranslationPlaybackFailureDoesNotDiscardRecording(t *testing.T) {
	s := newTerminalTranslationSessionFixture()
	options := terminalTranslationFixtureOptions(s)
	options.Listen = true
	options.Play = func(context.Context, io.ReadCloser, int, int) error {
		return errors.New("private helper path and diagnostic")
	}
	s.emit(terminalTranslationFixtureAudio([]byte{1, 2}))
	stop := make(chan struct{})
	var once sync.Once
	result := runTerminalTranslation(terminalTranslationTestContext(t), stop, options, func(update terminalTranslationUpdate) {
		if update.Warning != "" {
			once.Do(func() { close(stop) })
		}
	})
	t.Cleanup(func() { _ = result.Close() })
	require.NoError(t, result.Err)
	require.True(t, result.Complete)
	require.NotEmpty(t, result.Warning)
	require.NotContains(t, result.Warning, "private")
	require.Equal(t, []byte{1, 2}, terminalTranslationReadFile(t, result.AudioPath))
}

func TestTerminalTranslationStopDuringOpeningJoinsWithoutCapture(t *testing.T) {
	stop, entered, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	options := terminalTranslationOptions{
		Connect: func(ctx context.Context, _ audiolive.Config) (audiolive.Session, error) {
			defer close(returned)
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Capture: func(context.Context, string, func([]byte) error) error { panic("unexpected microphone capture") },
	}
	go func() { <-entered; close(stop) }()
	result := runTerminalTranslation(terminalTranslationTestContext(t), stop, options, nil)
	require.NoError(t, result.Err)
	require.Empty(t, result.Directory)
	select {
	case <-returned:
	default:
		t.Fatal("Connect remained active after return")
	}
	options.Connect = func(context.Context, audiolive.Config) (audiolive.Session, error) { panic("already stopped") }
	result = runTerminalTranslation(terminalTranslationTestContext(t), stop, options, nil)
	require.NoError(t, result.Err)
}

func TestTerminalTranslationRecordingCleanupPreservesUnownedFiles(t *testing.T) {
	stage, err := newTerminalTranslationStage()
	require.NoError(t, err)
	require.NoError(t, stage.finish())
	extra := filepath.Join(stage.directory, "keep.txt")
	require.NoError(t, os.WriteFile(extra, []byte("unowned"), 0600))
	t.Cleanup(func() { _ = os.Remove(extra); _ = os.Remove(stage.directory) })
	if runtime.GOOS != "windows" {
		for _, name := range stage.names {
			info, err := os.Stat(filepath.Join(stage.directory, name))
			require.NoError(t, err)
			require.Zero(t, info.Mode().Perm()&0077)
		}
	}
	require.Error(t, stage.close())
	require.Equal(t, "unowned", string(terminalTranslationReadFile(t, extra)))
	for _, name := range stage.names {
		_, err := os.Stat(filepath.Join(stage.directory, name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestTerminalTranslationRecordingReaderPinsOriginalAndCleanupPreservesReplacement(t *testing.T) {
	stage, err := newTerminalTranslationStage()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = stage.close()
		_ = os.Remove(filepath.Join(stage.directory, "translation.pcm"))
		_ = os.Remove(filepath.Join(stage.directory, "moved.pcm"))
		_ = os.Remove(stage.directory)
	})
	_, err = stage.files[0].Write([]byte{1, 2, 3, 4})
	require.NoError(t, err)
	_, err = stage.reader("translation.pcm")
	require.Error(t, err, "export must wait for worker completion")
	require.NoError(t, stage.finish())
	path := filepath.Join(stage.directory, "translation.pcm")
	require.NoError(t, os.Rename(path, filepath.Join(stage.directory, "moved.pcm")))
	require.NoError(t, os.WriteFile(path, []byte("replacement"), 0600))
	reader, err := stage.reader("translation.pcm")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte{1, 2, 3, 4}, data)
	require.Error(t, stage.close())
	require.Equal(t, "replacement", string(terminalTranslationReadFile(t, path)))
	_, err = stage.reader("translation.pcm")
	require.Error(t, err, "Close must release the original descriptor")
}

func TestTerminalTranslationRecordingReaderRejectsChangedOriginal(t *testing.T) {
	stage, err := newTerminalTranslationStage()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stage.close() })
	_, err = stage.files[0].Write([]byte{1, 2})
	require.NoError(t, err)
	require.NoError(t, stage.finish())
	_, err = stage.files[0].Write([]byte{3, 4})
	require.NoError(t, err)
	_, err = stage.reader("translation.pcm")
	require.ErrorContains(t, err, "changed")
}

func TestTerminalTranslationPlaybackQueueDrainsOrUnblocks(t *testing.T) {
	queue := newTerminalTranslationPCMQueue()
	require.True(t, queue.offer([]byte{1, 2, 3, 4}))
	queue.finish()
	data, err := io.ReadAll(queue)
	require.NoError(t, err)
	require.Equal(t, []byte{1, 2, 3, 4}, data)
	require.False(t, queue.offer([]byte{5}))
	queue = newTerminalTranslationPCMQueue()
	done := make(chan error, 1)
	go func() { _, err := queue.Read(make([]byte, 1)); done <- err }()
	require.NoError(t, queue.Close())
	select {
	case err := <-done:
		require.ErrorIs(t, err, io.EOF)
	case <-terminalTranslationTestContext(t).Done():
		t.Fatal("Close did not unblock Read")
	}
}
