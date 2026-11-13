package audio

import (
	"context"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/openai/openai-cli/internal/audiomedia"
)

// Playback reads bytes already written to the session's private recording.
// Only the session owns the file. One player reads at a time.
type terminalTranslationPCMReader struct {
	file          *os.File
	mu            sync.Mutex
	ready         *sync.Cond
	offset, end   int64
	ended, closed bool
	readEOF       bool
}

func newTerminalTranslationPCMReader(file *os.File) *terminalTranslationPCMReader {
	p := &terminalTranslationPCMReader{file: file}
	p.ready = sync.NewCond(&p.mu)
	return p
}

func (p *terminalTranslationPCMReader) publish(end int64) {
	p.mu.Lock()
	p.end = end
	p.ready.Signal()
	p.mu.Unlock()
}

func (p *terminalTranslationPCMReader) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	p.mu.Lock()
	for p.offset == p.end && !p.closed && !p.ended {
		p.ready.Wait()
	}
	if p.closed || p.offset == p.end {
		if !p.closed {
			p.readEOF = true
		}
		p.mu.Unlock()
		return 0, io.EOF
	}
	offset := p.offset
	data = data[:min(int64(len(data)), p.end-offset)]
	p.offset += int64(len(data))
	p.mu.Unlock()

	// ReadAt leaves the recorder's write offset alone. Release the lock first
	// so playback never makes the session wait to publish more data.
	n, err := p.file.ReadAt(data, offset)
	if err == io.EOF && n < len(data) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (p *terminalTranslationPCMReader) finish() {
	p.mu.Lock()
	p.ended = true
	p.ready.Broadcast()
	p.mu.Unlock()
}

func (p *terminalTranslationPCMReader) drained() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readEOF
}

func (p *terminalTranslationPCMReader) Close() error {
	p.mu.Lock()
	p.closed = true
	p.ready.Broadcast()
	p.mu.Unlock()
	return nil
}

func (r *terminalTranslationResult) duration() time.Duration {
	return terminalTranslationDuration(r.AudioBytes, r.SampleRate, r.Channels)
}

func terminalTranslationDuration(audioBytes int64, sampleRate, channels int) time.Duration {
	if audioBytes <= 0 || channels <= 0 || sampleRate <= 0 {
		return 0
	}
	frames := audioBytes / int64(channels) / 2
	rate := int64(sampleRate)
	seconds, remainder := frames/rate, frames%rate
	if seconds >= math.MaxInt64/int64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds)*time.Second + time.Duration(remainder)*time.Second/time.Duration(rate)
}

func (r *terminalTranslationResult) playFrom(ctx context.Context, offset time.Duration) error {
	if r.stage == nil || r.AudioBytes <= 0 {
		return audioFailure("There is no translated audio to play.", nil)
	}
	audio, err := r.stage.reader("translation.pcm")
	if err != nil {
		return err
	}
	return audiomedia.PlayPCMFrom(ctx, audio, r.SampleRate, r.Channels, offset)
}
