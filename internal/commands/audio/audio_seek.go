package audio

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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

func (m *audioWorkbench) waveformWidth() int {
	return min(math.MaxUint16, max(3, m.audioWidth()-4))
}

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
	m.selectPlaybackPosition(position)
}

func (m *audioWorkbench) selectPlaybackPosition(position time.Duration) {
	if position == m.result.playbackPosition() {
		return
	}
	if m.playing || m.playCancel != nil {
		m.stopPlayback()
	}
	m.result.position = position
	m.result.resumable = false
	m.result.finished = false
}

var audioSeekTimestamp = regexp.MustCompile(`^(?:[0-9]+:[0-5]?[0-9]|[0-9]+)(?:\.[0-9]{1,9})?$`)

func parseAudioSeekTime(text string) (time.Duration, bool) {
	value := strings.TrimSpace(text)
	if !audioSeekTimestamp.MatchString(value) {
		return 0, false
	}
	position, err := time.ParseDuration(strings.Replace(value, ":", "m", 1) + "s")
	return position, err == nil
}

// Keep the displayed milliseconds and any finer precision needed for a round trip.
func audioSeekClock(position time.Duration) string {
	return fmt.Sprintf("%02d:%02d.%03d", position/time.Minute, position/time.Second%60, position/time.Millisecond%1000) +
		strings.TrimRight(fmt.Sprintf("%06d", position%time.Millisecond), "0")
}

func (m *audioWorkbench) openSeek(initial string) tea.Cmd {
	if m.result.seekDuration() == 0 {
		return nil
	}
	m.capturePlaybackProgress()
	m.modal, m.seekError = "seek", ""
	m.seekInput.SetValue(initial)
	return m.seekInput.Focus()
}

func (m *audioWorkbench) closeSeek() {
	m.modal, m.seekError = "", ""
	m.seekInput.Blur()
}

func (m *audioWorkbench) updateSeek(message tea.Msg) tea.Cmd {
	before := m.seekInput.Value()
	var cmd tea.Cmd
	m.seekInput, cmd = m.seekInput.Update(message)
	if m.seekInput.Value() != before {
		m.seekError = ""
	}
	return cmd
}

func (m *audioWorkbench) applySeek() {
	// Rejecting a draft must not pause playback or rewind a newer player report.
	m.capturePlaybackProgress()
	target, ok := parseAudioSeekTime(m.seekInput.Value())
	if !ok || target > m.result.seekDuration() {
		m.seekError = "Use seconds or M:SS."
		if ok {
			m.seekError = "Time exceeds the recording."
		}
		m.seekInput.SetValue(audioSeekClock(m.result.playbackPosition()))
		m.seekInput.SelectAll()
		return
	}
	// An unchanged, restored value dismisses the editor while playback continues.
	if m.seekError == "" {
		m.selectPlaybackPosition(target)
		// Explicitly selecting the completed endpoint must not replay from zero.
		m.result.finished = false
	}
	m.closeSeek()
}

const audioSeekLabel = "  Go to time  "

func (m *audioWorkbench) seekView() tea.View {
	lines := []string{
		m.strong().Render("  Go to time"),
		"  " + m.muted().Render("Current  "+audioSeekClock(m.result.playbackPosition())),
		"  " + m.muted().Render("End      "+audioSeekClock(m.result.seekDuration())),
		"",
		"  " + m.editorView(m.seekInput),
		"  " + m.muted().Render("Enter 2.5 or 1:02.500."),
	}
	if m.seekError != "" {
		lines = append(lines, "  "+m.failureStyle().Render(m.seekError))
	}
	lines = append(lines, m.shortcutLine([2]string{"Enter", "apply"}, [2]string{"Esc", "cancel"}))
	return m.cursorView(lines, 1, audioEditorCursor(m.seekInput, 2, 4))
}
