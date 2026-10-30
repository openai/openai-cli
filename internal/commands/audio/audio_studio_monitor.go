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
	// Keep the lamp and the start of its label in fixed columns.
	lampColumn := width - 2 - ansi.StringWidth("◉ Generating")
	rows[0] = title + strings.Repeat(" ", max(1, lampColumn-ansi.StringWidth(title))) + m.playerStatus()
	if m.result != nil {
		rows[1] = "  " + m.recordingMetadata(width-4)
	} else if m.playerNotice != "" {
		rows[1] = "  " + m.warningStyle().Render(m.playerNotice)
	}

	// Four rows after the transport can carry a save receipt, an error with F1,
	// and two rows identifying a retained recording when the draft changes.
	const plotTop = 3
	budget := min(audioMaxWaveformHeight+4, height-plotTop-6)
	transport := plotTop + budget
	if m.result.hasWaveform() {
		// Reserve two rows for the inset rail and one each for the rail and scale.
		h := m.waveformDetailHeight(budget - 4)
		copy(rows[plotTop:transport], m.waveformRowsAtHeight(budget, h))
	} else {
		middle := plotTop + (budget-1)/2
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
	rows[transport] = "  " + timing
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
