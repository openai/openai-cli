package audio

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/audiomedia"
	clioutput "github.com/openai/openai-cli/internal/output"
)

type terminalTranslationOptions struct {
	Config        audiolive.Config
	Device        string
	File          *os.File
	Listen        bool
	Connect       func(context.Context, audiolive.Config) (audiolive.Session, error)
	Capture       func(context.Context, string, func([]byte) error) error
	Play          func(context.Context, io.ReadCloser, int, int) error
	DrainTimeout  time.Duration
	AppendTimeout time.Duration
}

// Caption fields are bounded display tails. The result's files retain complete captions.
type terminalTranslationUpdate struct {
	State                     string
	Original, Translation     string
	AudioBytes                int64
	SampleRate, Channels      int
	Tokens                    int64
	InputTokens, OutputTokens *int64
	Latency                   time.Duration
	HasLatency                bool
	Peaks                     []float64
	Warning                   string
}

// AudioPath contains unmodified, signed little-endian PCM16, without a WAV header.
// Ownership transfers to the caller only after every worker and writer has stopped.
type terminalTranslationResult struct {
	Directory, AudioPath, OriginalPath, TranslationPath string
	SampleRate, Channels                                int
	AudioBytes                                          int64
	Complete                                            bool
	Err                                                 error
	Warning                                             string
	stage                                               *terminalTranslationStage
}

// Close removes only this recording's files. It never recursively removes a directory.
func (r *terminalTranslationResult) Close() error {
	if r.stage == nil {
		return nil
	}
	return r.stage.close()
}

type terminalTranslationStage struct {
	directory  string
	root       *os.Root
	files      []*os.File
	names      []string
	identities []os.FileInfo
	sealed     []os.FileInfo
	finished   bool
	finishErr  error
	once       sync.Once
	err        error
}

func newTerminalTranslationStage() (*terminalTranslationStage, error) {
	directory, err := terminalTranslationDirectory("", "openai-translation-")
	if err != nil {
		return nil, err
	}
	s := &terminalTranslationStage{directory: directory}
	s.root, err = os.OpenRoot(directory)
	if err != nil {
		_ = os.Remove(directory)
		return nil, err
	}
	for _, name := range []string{"translation.pcm", "original.txt", "translation.txt", "input-fragments"} {
		file, err := clioutput.CreatePrivateDownloadFile(s.root, name)
		if err != nil {
			_ = s.finish()
			_ = s.close()
			return nil, err
		}
		s.names = append(s.names, name)
		s.files = append(s.files, file)
		info, err := file.Stat()
		s.identities = append(s.identities, info)
		if err != nil {
			_ = s.close()
			return nil, err
		}
	}
	return s, nil
}

func (s *terminalTranslationStage) finish() error {
	if s.finished {
		return s.finishErr
	}
	s.finished = true
	for _, file := range s.files {
		s.finishErr = errors.Join(s.finishErr, file.Sync())
		info, err := file.Stat()
		s.finishErr = errors.Join(s.finishErr, err)
		s.sealed = append(s.sealed, info)
	}
	return s.finishErr
}

// reader pins export to the descriptor written by this session. A replaced name,
// symlink, or FIFO cannot redirect it. The caller must finish reading before Close.
func (s *terminalTranslationStage) reader(name string) (*io.SectionReader, error) {
	if !s.finished {
		return nil, audioFailure("Translation files are not ready to save.", s.finishErr)
	}
	for i, candidate := range s.names {
		if candidate != name {
			continue
		}
		info, err := s.files[i].Stat()
		if err != nil {
			return nil, audioFailure("Could not read the retained translation file.", err)
		}
		sealed := s.sealed[i]
		if sealed == nil || !info.Mode().IsRegular() || info.Size() != sealed.Size() || !info.ModTime().Equal(sealed.ModTime()) {
			return nil, audioFailure("The retained translation file changed. Stop before saving another recording.", nil)
		}
		return io.NewSectionReader(s.files[i], 0, info.Size()), nil
	}
	return nil, audioFailure("The requested translation file is unavailable.", nil)
}

func (s *terminalTranslationStage) close() error {
	s.once.Do(func() {
		for _, file := range s.files {
			s.err = errors.Join(s.err, file.Close())
		}
		for i, name := range s.names {
			info, err := s.root.Lstat(name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || s.identities[i] == nil || !info.Mode().IsRegular() || !os.SameFile(info, s.identities[i]) {
				s.err = errors.Join(s.err, err, errors.New("temporary file identity changed"))
				continue
			}
			if err := s.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.err = errors.Join(s.err, err)
			}
		}
		info, statErr := s.root.Stat(".")
		pathInfo, pathErr := os.Lstat(s.directory)
		s.err = errors.Join(s.err, s.root.Close())
		if statErr == nil && pathErr == nil && os.SameFile(info, pathInfo) {
			s.err = errors.Join(s.err, os.Remove(s.directory))
		} else if !errors.Is(pathErr, os.ErrNotExist) {
			s.err = errors.Join(s.err, statErr, pathErr, errors.New("temporary directory identity changed"))
		}
		if s.err != nil {
			s.err = audioFailure("Could not remove the temporary translation files. Check temporary-folder access.", s.err)
		}
	})
	return s.err
}

type terminalTranslationReceived struct {
	event audiolive.Event
	err   error
}

type terminalTranslationCaptured struct {
	err, appendErr error
}

// runTerminalTranslation starts only when called. Stop flushes capture and drains
// the session; ctx cancellation aborts it. Injected helpers must honor cancellation
// and join their children before returning. notify runs serially, at most ten times
// per second except state transitions, and must return promptly without blocking.
func runTerminalTranslation(ctx context.Context, stop <-chan struct{}, options terminalTranslationOptions, notify func(terminalTranslationUpdate)) (result terminalTranslationResult) {
	if ctx.Err() != nil {
		result.Err = ctx.Err()
		return result
	}
	select {
	case <-stop:
		return result
	default:
	}
	update := terminalTranslationUpdate{State: "connecting"}
	var wave audiomedia.PCM16Envelope
	publish := func() {
		if notify != nil && ctx.Err() == nil {
			update.Peaks = wave.Peaks()
			notify(update)
		}
	}
	publish()
	captureAudio, source := options.Capture, options.Device
	if options.File != nil {
		captureAudio = func(ctx context.Context, _ string, onChunk func([]byte) error) error {
			return audiomedia.CaptureFilePCM(ctx, options.File, onChunk)
		}
	}
	if options.Connect == nil || captureAudio == nil || (options.Listen && options.Play == nil) {
		result.Err = audioFailure("Live translation cannot start because an audio helper is missing.", nil)
		return result
	}
	if options.DrainTimeout <= 0 {
		options.DrainTimeout = 15 * time.Second
	}
	if options.AppendTimeout <= 0 {
		options.AppendTimeout = 5 * time.Second
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	// Stop must also interrupt opening. Join the opener before returning ownership.
	opened := make(chan struct{})
	var session audiolive.Session
	var openErr error
	go func() {
		session, openErr = options.Connect(work, options.Config)
		close(opened)
	}()
	select {
	case <-opened:
	case <-stop:
		cancel()
		<-opened
	case <-ctx.Done():
		cancel()
		<-opened
	}
	if session != nil {
		defer session.Abort()
	}
	if work.Err() != nil {
		result.Err = ctx.Err() // An explicit Stop during opening is not an error.
		return result
	}
	select {
	case <-stop:
		return result
	default:
	}
	if openErr != nil || session == nil {
		result.Err = terminalTranslationFailure("Could not start live translation. Check the API connection and try again.", openErr)
		return result
	}
	stage, err := newTerminalTranslationStage()
	if err != nil {
		result.Err = audioFailure("Could not prepare translation files. Check temporary-folder access.", err)
		return result
	}
	result.stage, result.Directory = stage, stage.directory
	result.AudioPath = filepath.Join(stage.directory, stage.names[0])
	result.OriginalPath = filepath.Join(stage.directory, stage.names[1])
	result.TranslationPath = filepath.Join(stage.directory, stage.names[2])
	original := newTerminalInputCaptions(stage.files[3])
	var firstInput, firstOutput terminalCaptionTime

	captureCtx, cancelCapture := context.WithCancel(work)
	defer cancelCapture()
	captured := make(chan terminalTranslationCaptured, 1)
	go func() {
		var appendErr error
		err := captureAudio(captureCtx, source, func(data []byte) error {
			if appendErr != nil {
				return appendErr
			}
			if len(data)%2 != 0 {
				appendErr = audioFailure("The audio source returned an incomplete PCM16 sample.", nil)
				return appendErr
			}
			if len(data) == 0 {
				return nil
			}
			// Capture cancellation permits its final complete samples to reach Append.
			appendCtx, cancelAppend := context.WithTimeout(work, options.AppendTimeout)
			appendErr = session.Append(appendCtx, data)
			cancelAppend()
			return appendErr
		})
		captured <- terminalTranslationCaptured{err: err, appendErr: appendErr}
	}()
	received := make(chan terminalTranslationReceived, 1)
	receiverDone := make(chan struct{})
	go func() {
		defer close(receiverDone)
		for {
			event, err := session.Receive(work)
			select {
			case received <- terminalTranslationReceived{event, err}:
			case <-work.Done():
				return
			}
			if err != nil || event.Type == "session.closed" || event.Type == "error" {
				return
			}
		}
	}()
	var finishing <-chan error
	var finishDone chan error
	var playback *terminalTranslationPCMQueue
	var playing <-chan error
	var playDone chan error
	var cancelPlayback context.CancelFunc
	var deadline <-chan time.Time
	var timer *time.Timer
	stopping, closed, captureJoined := false, false, false
	setError := func(message string, err error) {
		result.Err = errors.Join(result.Err, terminalTranslationFailure(message, err))
	}
	stopCapture := func() {
		if stopping {
			return
		}
		stopping, stop = true, nil
		timer = time.NewTimer(options.DrainTimeout)
		deadline = timer.C
		update.State = "stopping"
		cancelCapture()
		publish()
	}
	disablePlayback := func(message string) {
		if update.Warning == "" {
			update.Warning = message
		}
		if playback != nil {
			_ = playback.Close()
			cancelPlayback()
		}
	}
	// One exit owns every worker. No caller can save or remove unfinished files.
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		cancelCapture()
		cancel()
		session.Abort()
		if !captureJoined {
			<-captured
		}
		<-receiverDone
		if finishing != nil {
			<-finishDone
		}
		if playing != nil {
			_ = playback.Close()
			cancelPlayback()
			<-playDone
		}
		if err := original.writeTo(ctx, stage.files[1]); err != nil {
			setError("Could not finish the original transcript.", err)
		}
		if err := stage.finish(); err != nil {
			setError("Could not finish writing the translation files. Received files remain available.", err)
		}
		result.AudioBytes, result.SampleRate, result.Channels = update.AudioBytes, update.SampleRate, update.Channels
		result.Warning = update.Warning
		result.Complete = closed && result.Err == nil
		update.State = "failed"
		if result.Complete {
			update.State = "complete"
		}
		publish()
	}()

	update.State = "listening"
	publish()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Err = errors.Join(result.Err, ctx.Err())
			return result
		case <-stop:
			stopCapture()
		case <-deadline:
			if closed && captureJoined && finishing == nil {
				disablePlayback("Live playback did not finish before the stop timeout. Play the saved audio after stopping.")
				return result
			}
			setError("Translation did not finish before the stop timeout. Received audio and captions remain available.", context.DeadlineExceeded)
			return result
		case capture := <-captured:
			captureJoined = true
			captured = nil
			if capture.appendErr != nil {
				setError("Could not send input audio. Received audio and captions remain available.", capture.appendErr)
				return result
			}
			if capture.err != nil && !(stopping && errors.Is(capture.err, context.Canceled)) {
				message := "Microphone capture stopped. Check microphone access and the selected device."
				if options.File != nil {
					message = "Could not finish reading the audio file. Check its format and permissions."
				}
				setError(message, capture.err)
			}
			stopCapture()
			if err := ctx.Err(); err != nil {
				result.Err = errors.Join(result.Err, err)
				return result
			}
			finishDone = make(chan error, 1)
			finishing = finishDone
			go func() { finishDone <- session.FinishInput(work) }()
		case err := <-finishing:
			finishing = nil
			if err != nil {
				setError("Could not finish sending input audio. Received audio and captions remain available.", err)
				return result
			}
		case item := <-received:
			if item.err != nil {
				setError("The translation connection stopped before completion. Received audio and captions remain available.", item.err)
				return result
			}
			event := item.event
			if event.Tokens > 0 {
				update.Tokens = event.Tokens
			}
			if event.InputTokens != nil {
				update.InputTokens = event.InputTokens
			}
			if event.OutputTokens != nil {
				update.OutputTokens = event.OutputTokens
			}
			switch event.Type {
			case "error":
				setError("The service stopped translation. Received audio and captions remain available.", audiolive.Failure(event.Message))
				return result
			case "session.closed":
				closed = true
				received = nil
				stopCapture()
			case "session.input_transcript.delta", "session.input_transcript.completed":
				tail, err := original.receive(event)
				if err != nil {
					setError("Could not save the original transcript.", err)
					return result
				}
				update.Original = tail
				if tail != "" {
					firstInput.observe(event)
				}
			case "session.output_transcript.delta":
				if err := terminalTranslationCopy(work, stage.files[2], strings.NewReader(event.Delta)); err != nil {
					setError("Could not save translation captions. Received files remain available.", err)
					return result
				}
				update.Translation = terminalTranslationTail(update.Translation, event.Delta)
				if event.Delta != "" {
					firstOutput.observe(event)
				}
			case "session.output_audio.delta":
				rate, channels, err := terminalTranslationAudioMetadata(event, update)
				if err != nil {
					setError("The service returned unsupported or inconsistent PCM16 audio. Earlier received files remain available.", err)
					return result
				}
				counter := &terminalTranslationCount{}
				if err := terminalTranslationCopy(work, counter, base64.NewDecoder(base64.StdEncoding, strings.NewReader(event.Delta))); err != nil || counter.bytes%int64(channels*2) != 0 {
					setError("The service returned incomplete audio. Earlier received files remain available.", err)
					return result
				}
				if counter.bytes == 0 {
					continue
				}
				update.SampleRate, update.Channels = rate, channels
				if options.Listen && playback == nil && update.Warning == "" {
					playback = newTerminalTranslationPCMQueue()
					playCtx, stopPlayback := context.WithCancel(work)
					cancelPlayback = stopPlayback
					playDone = make(chan error, 1)
					playing = playDone
					go func() {
						err := options.Play(playCtx, playback, rate, channels)
						_ = playback.Close()
						playDone <- err
					}()
				}
				writer := terminalTranslationAudioWriter{file: stage.files[0], update: &update, wave: &wave, playback: playback, overrun: func() {
					disablePlayback("Live playback could not keep up and stopped. Recording continues; play the saved audio after stopping.")
				}}
				if err := terminalTranslationCopy(work, &writer, base64.NewDecoder(base64.StdEncoding, strings.NewReader(event.Delta))); err != nil {
					setError("Could not save translated audio. Received files remain available.", err)
					return result
				}
			}
			update.Latency, update.HasLatency = firstInput.delayUntil(firstOutput)
		case err := <-playing:
			playing = nil
			if !playback.drained() || err != nil {
				disablePlayback("Live playback stopped. Recording continues; play the saved audio after stopping.")
			}
		case <-tick.C:
			publish()
		}
		if closed && captureJoined && finishing == nil {
			if playing == nil {
				return result
			}
			playback.finish()
		}
	}
}

func terminalTranslationFailure(message string, err error) error {
	var safe audiolive.Failure
	if errors.As(err, &safe) && safe != "" {
		message = safe.Error()
	}
	return audioFailure(message, err)
}

func terminalTranslationAudioMetadata(event audiolive.Event, update terminalTranslationUpdate) (int, int, error) {
	rate, channels := event.SampleRate, event.Channels
	if rate == 0 {
		rate = 24000
	}
	if channels == 0 {
		channels = 1
	}
	if (event.Format != "" && event.Format != "pcm16") || rate < 1 || channels < 1 || channels > 65535/2 || uint64(rate) > uint64(^uint32(0))/uint64(channels*2) || (update.AudioBytes > 0 && (update.SampleRate != rate || update.Channels != channels)) {
		return 0, 0, errors.New("invalid PCM16 metadata")
	}
	return rate, channels, nil
}

func terminalTranslationCopy(ctx context.Context, dst io.Writer, src io.Reader) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := src.Read(buffer)
		if n > 0 {
			written, writeErr := dst.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

type terminalTranslationCount struct{ bytes int64 }

func (w *terminalTranslationCount) Write(data []byte) (int, error) {
	w.bytes += int64(len(data))
	return len(data), nil
}

type terminalTranslationAudioWriter struct {
	file     *os.File
	update   *terminalTranslationUpdate
	wave     *audiomedia.PCM16Envelope
	playback *terminalTranslationPCMQueue
	overrun  func()
}

func (w *terminalTranslationAudioWriter) Write(data []byte) (int, error) {
	n, err := w.file.Write(data)
	w.update.AudioBytes += int64(n)
	w.wave.Add(data[:n], w.update.Channels)
	if n > 0 && w.playback != nil && !w.playback.offer(data[:n]) {
		w.overrun()
	}
	return n, err
}

func terminalTranslationTail(previous, delta string) string {
	const limit = 4096
	if len(delta) >= limit {
		previous, delta = "", delta[len(delta)-limit:]
	} else if len(previous)+len(delta) <= limit {
		return previous + delta
	} else {
		previous = previous[len(previous)+len(delta)-limit:]
	}
	tail := previous + delta
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return "…" + strings.Clone(tail)
}

// The live playback queue is optional and bounded independently of saved audio.
// finish drains it to EOF. Close discards it and unblocks every pending Read.
type terminalTranslationPCMQueue struct {
	mu            sync.Mutex
	ready         *sync.Cond
	buffer        []byte
	start, length int
	ended, closed bool
	readEOF       bool
}

func newTerminalTranslationPCMQueue() *terminalTranslationPCMQueue {
	q := &terminalTranslationPCMQueue{buffer: make([]byte, 256*1024)}
	q.ready = sync.NewCond(&q.mu)
	return q
}

func (q *terminalTranslationPCMQueue) offer(data []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.ended || len(data) > len(q.buffer)-q.length {
		return false
	}
	end := (q.start + q.length) % len(q.buffer)
	n := copy(q.buffer[end:], data)
	copy(q.buffer, data[n:])
	q.length += len(data)
	q.ready.Signal()
	return true
}

func (q *terminalTranslationPCMQueue) Read(data []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(data) == 0 {
		return 0, nil
	}
	for q.length == 0 && !q.closed && !q.ended {
		q.ready.Wait()
	}
	if q.closed {
		return 0, io.EOF
	}
	if q.length == 0 {
		q.readEOF = true
		return 0, io.EOF
	}
	n := min(len(data), q.length, len(q.buffer)-q.start)
	copy(data, q.buffer[q.start:q.start+n])
	q.start, q.length = (q.start+n)%len(q.buffer), q.length-n
	return n, nil
}

func (q *terminalTranslationPCMQueue) finish() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ended = true
	q.ready.Broadcast()
}

func (q *terminalTranslationPCMQueue) drained() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.readEOF
}

func (q *terminalTranslationPCMQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed, q.length = true, 0
	q.ready.Broadcast()
	return nil
}
