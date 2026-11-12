package audio

import (
	"context"
	"math"
	"time"

	"github.com/openai/openai-cli/internal/audiomedia"
)

func (r *terminalTranslationResult) duration() time.Duration {
	if r.AudioBytes <= 0 || r.Channels <= 0 || r.SampleRate <= 0 {
		return 0
	}
	frames := r.AudioBytes / int64(r.Channels) / 2
	rate := int64(r.SampleRate)
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
