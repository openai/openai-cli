package audio

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The monitor reserves its plot and transport independently of the recording state.
func (m *audioWorkbench) studioMonitorRows(width, height int) ([]string, *tea.Cursor) {
	rows := make([]string, height)
	title := m.composerHeading("Recording", m.focus == 4)
	// Keep the status aligned with the end of the timeline.
	statusColumn := width - 2 - audioPlayerStatusWidth
	rows[0] = title + strings.Repeat(" ", max(1, statusColumn-ansi.StringWidth(title))) + m.playerStatus()
	if m.result != nil {
		rows[1] = "  " + m.recordingMetadata(width-4)
	} else if m.playerNotice != "" {
		rows[1] = "  " + m.warningStyle().Render(m.playerNotice)
	}

	// Four rows after the transport can carry a save receipt, an error summary,
	// and two rows identifying a retained recording when the draft changes.
	const plotTop = 3
	budget := min(m.waveformDetailHeight(audioMaxWaveformHeight)+3, height-plotTop-6)
	transport := plotTop + budget
	// Both panes reserve identical timeline geometry as the recording state changes.
	plotHeight := m.waveformDetailHeight(budget - 2)
	copy(rows[plotTop:transport], m.waveformRowsAtHeight(budget, plotHeight))
	if !m.result.hasWaveform() {
		middle := plotTop + plotHeight/2
		message := "No recording yet"
		switch {
		case m.result != nil:
			message = "Preview unavailable"
		case m.busy && m.note == "Working: generate":
			message = "Waiting for audio"
		default:
			hint := "Generate speech to see the waveform."
			rows[middle+1] = strings.Repeat(" ", (width-len(hint))/2) + m.muted().Render(hint)
		}
		rows[middle] = strings.Repeat(" ", (width-len(message))/2) + m.primaryText().Render(message)
	}
	timing, _ := m.playerTiming()
	rows[transport] = timing
	rows[transport+1] = m.playerActions()
	var cursor *tea.Cursor
	if m.modal == "seek" {
		clocks := "Current  " + audioSeekClock(m.result.playbackPosition()) + "  End  " + audioSeekClock(m.result.seekDuration())
		rows[transport] = "  " + m.muted().Render(clocks)
		rows[transport+1] = m.primaryText().Render(audioSeekLabel) + m.editorView(m.seekInput)
		cursor = audioEditorCursor(m.seekInput, len(audioSeekLabel), transport+1)
	}
	var notes []string
	if m.modal != "seek" {
		if path := m.savedRecordingRow(width); path != "" {
			notes = append(notes, path)
		}
	}
	if issue := m.retainedGenerationErrorRow(width); issue != "" {
		notes = append(notes, issue)
	}
	notes = append(notes, m.workspaceRecordingRows()...)
	copy(rows[transport+2:], notes)
	for i, line := range rows {
		rows[i] = ansi.Truncate(line, width, "…")
	}
	return rows, cursor
}
