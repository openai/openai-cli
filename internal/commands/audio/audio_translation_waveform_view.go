package audio

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *terminalTranslationModel) translationAudioHeading(layout translationLayout) string {
	duration := terminalTranslationDuration(m.state.AudioBytes, m.state.SampleRate, m.state.Channels)
	if m.finished {
		duration = m.result.duration()
	}
	if duration <= 0 {
		return ""
	}

	timing := audioSeekClock(duration) + " received"
	if m.finished {
		action := "Seek "
		if m.replayDone != nil {
			action = "From "
		}
		timing = action + audioSeekClock(m.seekPosition) + " / " + audioSeekClock(duration)
	}
	label := "Translated audio"
	if len(label)+len(timing)+2 > layout.width {
		label = "Audio"
		timing = ansi.Truncate(timing, layout.width-len(label)-2, "…")
	}
	return layout.heading(layout.muted.Render(label), timing)
}

func (m *terminalTranslationModel) translationWaveformRows(layout translationLayout, budget int) []string {
	if len(m.state.Peaks) == 0 || budget < 3 {
		return nil
	}
	height := 3
	if layout.expanded {
		height = min(7, budget)
	}
	marker := -1
	if m.finished && m.replayDone == nil {
		if duration := m.result.duration(); duration > 0 {
			position := min(duration, max(0, m.seekPosition))
			marker = int(math.Round(float64(position) / float64(duration) * float64(layout.width-1)))
		}
	}

	// A fixed display curve keeps quiet speech visible without rescaling earlier bars.
	peaks := make([]float64, len(m.state.Peaks))
	for i, peak := range m.state.Peaks {
		peaks[i] = math.Sqrt(peak)
	}
	wave, _ := measuredAudioWaveformRows(peaks, layout.width, height, marker, true, 1)
	if !m.color || len(wave) == 0 {
		return wave
	}

	axis := m.resultColor("#BAC4D8", "#51566B")
	seek := m.resultColor("#20283B", "#F2F4FA")
	shades := lipgloss.Blend1D(len(wave)/2+1, axis.GetForeground(), layout.muted.GetForeground())
	for row, line := range wave {
		style := lipgloss.NewStyle().Foreground(shades[min(row, len(wave)-1-row)])
		left, right, hasMarker := strings.Cut(line, "┃")
		if hasMarker {
			wave[row] = style.Render(left) + seek.Render("┃") + style.Render(right)
		} else {
			wave[row] = style.Render(line)
		}
	}
	return wave
}
