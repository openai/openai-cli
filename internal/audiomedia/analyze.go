// Package audiomedia analyzes immutable local audio and owns cancellable playback.
// Callers retain ownership of the verified snapshot and must not modify it while
// an operation runs. The package never reopens its pathname or closes its file.
package audiomedia

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strings"
)

var (
	ErrInvalidAudio       = errors.New("audio is malformed or incomplete")
	ErrUnsupportedFormat  = errors.New("audio format is unsupported")
	ErrAnalysisFailed     = errors.New("audio analysis failed")
	ErrPlaybackFailed     = errors.New("audio playback failed")
	ErrDecoderUnavailable = errors.New("audio decoding requires FFmpeg; install FFmpeg or use uncompressed WAV or PCM")
	ErrPlayerUnavailable  = errors.New("audio playback requires FFplay; install FFmpeg with FFplay")
	errNativeUnsupported  = errors.New("audio requires external decoding")
)

// UnavailableError identifies a missing optional helper without exposing paths.
type UnavailableError struct{ Tool string }

func (e *UnavailableError) Error() string { return e.Unwrap().Error() }
func (e *UnavailableError) Unwrap() error {
	if e.Tool == "ffplay" {
		return ErrPlayerUnavailable
	}
	return ErrDecoderUnavailable
}

type Analysis struct {
	Format   string    `json:"format"`
	Bytes    int64     `json:"bytes"`
	Duration float64   `json:"duration"`
	Peaks    []float64 `json:"peaks"`
}

// Analyze scans the supplied snapshot without changing its seek offset. A caller
// may use a context deadline to bound this optional work after preserving audio.
// Duration counts decoded frames. Each peak is the maximum absolute sample over
// all channels in its interval; opposite-phase stereo cannot cancel the waveform.
func Analyze(ctx context.Context, file *os.File, format string) (Analysis, error) {
	return analyze(ctx, file, format, systemHelpers())
}

func analyze(ctx context.Context, file *os.File, format string, helpers helpers) (Analysis, error) {
	format = strings.ToLower(format)
	result := Analysis{Format: format}
	size, err := snapshotSize(ctx, file)
	if err != nil {
		return result, err
	}
	result.Bytes = size
	if _, err := demuxer(format); err != nil {
		return result, err
	}
	var pcm pcmFormat
	var offset, length int64
	switch format {
	case "pcm":
		pcm = rawPCMFormat()
		length = size
	case "wav":
		var wave waveData
		wave, err = readWave(ctx, file, size)
		if err == nil {
			pcm, offset, length = wave.pcm, wave.offset, wave.length
		}
	default:
		err = errNativeUnsupported
	}
	var samples *pcmAnalyzer
	if errors.Is(err, errNativeUnsupported) {
		samples, err = decodeAnalysis(ctx, file, size, format, helpers)
	} else if err == nil {
		samples = newPCMAnalyzer(ctx, pcm)
		var copied int64
		copied, err = io.Copy(samples, &contextReader{ctx, io.NewSectionReader(file, offset, length)})
		if err == nil && copied != length {
			err = ErrInvalidAudio
		}
		if err == nil {
			err = samples.finish()
		}
		if err != nil {
			err = ErrInvalidAudio
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Duration = float64(samples.frames) / float64(samples.format.rate)
	result.Peaks = samples.peaks.finish()
	return result, nil
}

func snapshotSize(ctx context.Context, file *os.File) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if file == nil {
		return 0, ErrInvalidAudio
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 {
		return 0, ErrInvalidAudio
	}
	return info.Size(), nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

// The bins grow by powers of two. Memory stays bounded for arbitrary durations;
// the final bin may cover fewer frames. No audio or decoded-output size cap exists.
type peakBins struct {
	values []float64
	width  uint64
	count  uint64
	peak   float64
}

func (b *peakBins) add(peak float64) {
	if b.width == 0 {
		b.width = 1
	}
	if peak > b.peak {
		b.peak = peak
	}
	b.count++
	if b.count != b.width {
		return
	}
	b.values = append(b.values, b.peak)
	b.count, b.peak = 0, 0
	if len(b.values) < 512 {
		return
	}
	for i := 0; i < 256; i++ {
		b.values[i] = math.Max(b.values[2*i], b.values[2*i+1])
	}
	b.values = b.values[:256]
	b.width *= 2
}

func (b *peakBins) finish() []float64 {
	result := append([]float64(nil), b.values...)
	if b.count != 0 {
		result = append(result, b.peak)
	}
	if result == nil {
		result = []float64{}
	}
	if b.count != 0 && len(result) > 1 {
		// Uniform output intervals avoid stretching the shorter final bin. A
		// source maximum contributes to each overlapping interval, conservatively
		// retaining transients at the bounded summary's temporal resolution.
		total := uint64(len(b.values))*b.width + b.count
		count := uint64(len(result))
		uniform := make([]float64, len(result))
		boundary := func(index uint64) uint64 { return total/count*index + (total%count)*index/count }
		for index := range uniform {
			first := boundary(uint64(index)) / b.width
			last := (boundary(uint64(index+1)) - 1) / b.width
			for bin := first; bin <= last; bin++ {
				uniform[index] = math.Max(uniform[index], result[bin])
			}
		}
		result = uniform
	}
	return result
}
