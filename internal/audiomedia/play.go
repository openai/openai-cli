package audiomedia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

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
