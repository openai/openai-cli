package audio

import (
	"sync"
	"time"
)

// Each playback owns its sample. Replaced workers cannot update the active one.
type audioPlaybackProgress struct {
	mu       sync.Mutex
	position time.Duration
	reported bool
	entryID  string
}

func (p *audioPlaybackProgress) record(position time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if position >= 0 && (!p.reported || position >= p.position) {
		p.position, p.reported = position, true
	}
}

func (m *audioWorkbench) capturePlaybackProgress() {
	p := m.playProgress
	if !m.playing || m.result == nil || p == nil || m.result.entry.ID != p.entryID {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reported {
		m.result.position = min(m.result.seekDuration(), max(m.result.playbackPosition(), p.position))
	}
}
