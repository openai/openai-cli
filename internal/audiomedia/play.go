package audiomedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ValidatePlayback checks helper discovery without opening an output device.
func ValidatePlayback() error {
	if _, err := systemHelpers().find("ffplay"); err != nil {
		return ErrPlayerUnavailable
	}
	return nil
}

// PlayPCM plays a live signed little-endian PCM16 stream with explicit metadata.
// It owns source.Close on every exit. Close must unblock a pending Read; an
// io.PipeReader satisfies this contract. No input queue or recording is retained.
func PlayPCM(ctx context.Context, source io.ReadCloser, rate, channels int) error {
	return playPCM(ctx, source, rate, channels, systemHelpers())
}

func playPCM(ctx context.Context, source io.ReadCloser, rate, channels int, h helpers) error {
	if source == nil {
		return ErrInvalidAudio
	}
	var closeOnce sync.Once
	var closeErr error
	closeSource := func() { closeOnce.Do(func() { closeErr = source.Close() }) }
	defer closeSource()
	if err := ctx.Err(); err != nil {
		return err
	}
	header, err := streamPCMHeader(rate, channels)
	if err != nil {
		return err
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	stopClose := context.AfterFunc(work, closeSource)
	defer stopClose()
	args, _ := playerArguments("wav", false)
	command, err := helperCommand(work, h, "ffplay", args)
	if err != nil {
		return err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return ErrPlaybackFailed
	}
	defer read.Close()
	defer write.Close()
	diagnostics := &helperDiagnostics{}
	command.Stdin, command.Stdout, command.Stderr = read, io.Discard, diagnostics
	if err := command.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrPlaybackFailed
	}
	read.Close()
	pcm := &pcmStreamReader{source: source, alignment: channels * 2}
	copied := make(chan error, 1)
	go func() {
		_, err := io.CopyBuffer(write, io.MultiReader(bytes.NewReader(header), pcm), make([]byte, 32*1024))
		if err != nil {
			cancel()
		}
		write.Close()
		copied <- err
	}()
	runErr := command.Wait()
	stopHelperProcess(command)
	complete := pcm.complete.Load()
	// A player may exit before its source produces bytes. Close the source and
	// pipe before joining the copier, including a successful premature exit.
	cancel()
	write.Close()
	closeSource()
	copyErr := <-copied
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(copyErr, ErrInvalidAudio) {
		return ErrInvalidAudio
	}
	if runErr != nil || copyErr != nil || closeErr != nil || diagnostics.present || !complete {
		return ErrPlaybackFailed
	}
	return nil
}

// An open-ended WAV header carries channel metadata across FFplay versions.
// Raw PCM channel flags differ between old and current FFmpeg releases.
func streamPCMHeader(rate, channels int) ([]byte, error) {
	if rate <= 0 || channels <= 0 || channels > 65535/2 || uint64(rate) > uint64(^uint32(0))/uint64(channels*2) {
		return nil, ErrInvalidAudio
	}
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], ^uint32(0))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate)*uint32(channels)*2)
	binary.LittleEndian.PutUint16(header[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], ^uint32(0))
	return header, nil
}

type pcmStreamReader struct {
	source               io.Reader
	alignment, remainder int
	complete             atomic.Bool
}

func (r *pcmStreamReader) Read(data []byte) (int, error) {
	n, err := r.source.Read(data)
	r.remainder = (r.remainder + n) % r.alignment
	if err == io.EOF {
		if r.remainder != 0 {
			err = ErrInvalidAudio
		} else {
			r.complete.Store(true)
		}
	}
	return n, err
}

// Play starts local audio only when called explicitly. Native PCM/WAV offsets
// skip complete sample frames. Compressed offsets use output-side decoder seek.
// Every helper reads verified bytes through pipes; no path or URL reaches it.
func Play(ctx context.Context, file *os.File, format string, offset time.Duration) error {
	return play(ctx, file, format, offset, systemHelpers())
}

// PlayWithProgress reports positions from FFplay's audio clock in the original
// recording. The player estimates device buffering; reports are not exact audible
// positions. No report arrives until the player supplies a usable clock.
// Reports run serially, must return promptly, and stop before this function returns.
// Callers must reject stale reports when replacing playback and handle no reports.
// A nil report preserves Play's behavior. The caller still owns the file.
func PlayWithProgress(ctx context.Context, file *os.File, format string, offset time.Duration, report func(time.Duration)) error {
	return playWithProgress(ctx, file, format, offset, report, systemHelpers())
}

func play(ctx context.Context, file *os.File, format string, offset time.Duration, h helpers) error {
	return playWithProgress(ctx, file, format, offset, nil, h)
}

func playWithProgress(ctx context.Context, file *os.File, format string, offset time.Duration, report func(time.Duration), h helpers) error {
	if offset < 0 {
		return ErrInvalidAudio
	}
	format = strings.ToLower(format)
	size, err := snapshotSize(ctx, file)
	if err != nil {
		return err
	}
	if _, err := demuxer(format); err != nil {
		return err
	}
	switch format {
	case "pcm":
		if size%2 != 0 {
			return ErrInvalidAudio
		}
		skipped := offsetFrames(offset, 24000, size/2) * 2
		if skipped == size {
			return nil
		}
		return playReader(ctx, io.NewSectionReader(file, skipped, size-skipped), "pcm", frameOffset(skipped/2, 24000), report, h)
	case "wav":
		wave, err := readWave(ctx, file, size)
		if errors.Is(err, errNativeUnsupported) {
			break
		}
		if err != nil {
			return err
		}
		skipped := offsetFrames(offset, wave.pcm.rate, wave.length/int64(wave.pcm.align)) * int64(wave.pcm.align)
		if skipped == wave.length {
			return nil
		}
		length := wave.length - skipped
		// Rebuild only the container header. Original sample bytes and channel
		// metadata remain intact; the caller's file and seek position stay intact.
		formatBytes := append([]byte(nil), wave.fmt...)
		if len(formatBytes) > 16 && len(formatBytes) < 18 {
			return ErrInvalidAudio
		}
		if len(formatBytes) >= 18 {
			wave.pcm.order.PutUint16(formatBytes[16:18], uint16(len(formatBytes)-18))
		}
		header := make([]byte, 12+8+len(formatBytes)+len(formatBytes)%2+8)
		copy(header, "RIFF")
		if wave.bigEndian {
			copy(header, "RIFX")
		}
		wave.pcm.order.PutUint32(header[4:8], uint32(len(header)-8)+uint32(length)+uint32(length%2))
		copy(header[8:], "WAVEfmt ")
		wave.pcm.order.PutUint32(header[16:20], uint32(len(formatBytes)))
		copy(header[20:], formatBytes)
		copy(header[len(header)-8:], "data")
		wave.pcm.order.PutUint32(header[len(header)-4:], uint32(length))
		readers := []io.Reader{bytes.NewReader(header), io.NewSectionReader(file, wave.offset+skipped, length)}
		if length%2 != 0 {
			readers = append(readers, bytes.NewReader([]byte{0}))
		}
		return playReader(ctx, io.MultiReader(readers...), "wav", frameOffset(skipped/int64(wave.pcm.align), wave.pcm.rate), report, h)
	}
	return playDecoded(ctx, file, size, format, offset, report, h)
}

// frames comes from offsetFrames, so the result cannot exceed the valid offset.
func frameOffset(frames int64, rate uint32) time.Duration {
	return time.Duration(frames/int64(rate))*time.Second + time.Duration(frames%int64(rate))*time.Second/time.Duration(rate)
}

func offsetFrames(offset time.Duration, rate uint32, frames int64) int64 {
	seconds := int64(offset / time.Second)
	if seconds > frames/int64(rate) {
		return frames
	}
	whole := seconds * int64(rate)
	fraction := int64(offset%time.Second) * int64(rate) / int64(time.Second)
	if fraction > frames-whole {
		return frames
	}
	return whole + fraction
}

// PlayPCMFrom replays retained PCM16 starting at a complete sample frame.
// The caller owns the immutable recording and its underlying descriptor.
func PlayPCMFrom(ctx context.Context, source *io.SectionReader, rate, channels int, offset time.Duration) error {
	return playPCMFrom(ctx, source, rate, channels, offset, systemHelpers())
}

func playPCMFrom(ctx context.Context, source *io.SectionReader, rate, channels int, offset time.Duration, h helpers) error {
	if source == nil || offset < 0 {
		return ErrInvalidAudio
	}
	if _, err := streamPCMHeader(rate, channels); err != nil {
		return err
	}
	align := int64(channels) * 2
	if source.Size()%align != 0 {
		return ErrInvalidAudio
	}
	start := offsetFrames(offset, uint32(rate), source.Size()/align) * align
	if start == source.Size() {
		return ctx.Err()
	}
	return playPCM(ctx, io.NopCloser(io.NewSectionReader(source, start, source.Size()-start)), rate, channels, h)
}
