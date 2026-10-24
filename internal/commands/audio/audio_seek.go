package audio

import (
	"math"
	"strings"
	"time"
)

func (r *audioWorkResult) seekDuration() time.Duration {
	if r == nil || !r.measured || math.IsNaN(r.analysis.Duration) || math.IsInf(r.analysis.Duration, 0) ||
		r.analysis.Duration <= 0 || r.analysis.Duration >= float64(math.MaxInt64)/float64(time.Second) {
		return 0
	}
	return time.Duration(r.analysis.Duration * float64(time.Second))
}

func (r *audioWorkResult) playbackPosition() time.Duration {
	return max(0, min(r.position, r.seekDuration()))
}

func (m *audioWorkbench) waveformWidth() int { return min(72, max(3, m.audioWidth()-2)) }

func (m *audioWorkbench) selectedBoundary() int {
	duration := m.result.seekDuration()
	if duration <= 0 {
		return 0
	}
	bars := audioWaveformBars(m.waveformWidth())
	return int(math.Round(float64(m.result.playbackPosition()) / float64(duration) * float64(bars)))
}

// Seeking changes the next explicit playback start, never the retained bytes.
// During playback, FFplay's reported clock updates the selected position.
func (m *audioWorkbench) seekResult(key string) {
	m.capturePlaybackProgress()
	duration := m.result.seekDuration()
	if duration <= 0 {
		return
	}
	bars := audioWaveformBars(m.waveformWidth())
	step := 1
	if strings.HasPrefix(key, "shift+") {
		step = 5
	}
	if strings.HasSuffix(key, "left") {
		step = -step
	}
	boundary := int64(max(0, min(bars, m.selectedBoundary()+step)))
	count := time.Duration(bars)
	// Split before multiplying to preserve long recordings without overflow.
	position := duration/count*time.Duration(boundary) + duration%count*time.Duration(boundary)/count
	if position == m.result.playbackPosition() {
		return
	}
	if m.playing || m.playCancel != nil {
		m.stopPlayback()
	}
	m.result.position = position
	m.result.finished = false
}
