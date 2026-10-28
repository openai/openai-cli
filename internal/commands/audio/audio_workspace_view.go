package audio

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

// The framed workspace keeps the draft and retained recording together.
func (m *audioWorkbench) workspaceView() tea.View {
	rows := m.formRows(false, true)
	cursor := m.formCursor(false)
	cursorRow := -1
	if cursor != nil {
		cursorRow = cursor.Y
	}
	rows = append(rows, "")
	rows = append(rows, m.formActionRows()...)
	rows = append(rows, "")
	footer := m.workspaceFooter()
	// Most hints use one or two rows. Focus must not resize the player between them.
	footerRows := max(2, len(footer))
	header, controls := m.workspaceAudioHeader(), m.workspacePlayerControls()
	minimum := len(header) + len(controls)
	if m.result.hasWaveform() {
		minimum += 2 // One measured row and the seek rail.
	}
	contentRows := 0
	for _, row := range rows {
		if row != "" {
			contentRows++
		}
	}
	gapBudget := max(0, m.audioHeight()-contentRows-footerRows-minimum)
	var lines []string
	for index, row := range rows {
		if row == "" { // Editor rows retain their indentation, even when empty.
			if gapBudget == 0 {
				if cursor != nil && index < cursorRow {
					cursor.Y--
				}
				continue
			}
			gapBudget--
		}
		lines = append(lines, row)
	}
	lines = append(lines, header...)
	budget := m.audioHeight() - len(lines) - len(controls) - footerRows
	wave := m.workspaceWaveformRows(budget)
	lines = append(lines, wave...)
	if len(wave) > 0 {
		spare := budget - len(wave)
		if len(m.result.savedPaths) == 0 {
			spare-- // Let the first save receipt fit without moving either action.
		}
		if spare > 0 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, controls...)
	return m.cursorView(append(lines, footer...), len(footer), cursor)
}

func (m *audioWorkbench) workspaceFooter() []string {
	if m.busy {
		return m.formShortcutRows([2]string{"Ctrl+C", "cancel"})
	}
	if m.focus == 4 {
		return m.resultHintRows()
	}
	next := "Settings"
	if m.focus == 0 && m.instructionsSupported() {
		next = "Instructions"
	} else if m.focus == 2 {
		next = "Audio"
	}
	shortcuts := [][2]string{{"Ctrl+C", "Exit"}, {"Ctrl+G", "Generate"}, {"Tab", next}}
	if m.focus == 0 || m.focus == 1 {
		shortcuts = append(shortcuts, [2]string{"Alt+Enter", "New line"})
	} else if m.focus == 2 && m.setting < 4 {
		if m.speedEditing {
			return m.formShortcutRows([2]string{"Enter", "apply"}, [2]string{"Esc", "cancel"}, [2]string{"←→", "0.25"})
		}
		action := "change"
		if m.setting == 3 {
			action = "0.25"
		}
		shortcuts = append(shortcuts, [2]string{"←→", action})
		if m.setting == 3 {
			shortcuts = append(shortcuts, [2]string{"0–9", "type speed"})
		} else {
			shortcuts = append(shortcuts, [2]string{"Enter", "list"})
		}
	}
	shortcuts = append(shortcuts, [2]string{"Ctrl+O", "Audio"})
	return m.formShortcutRows(shortcuts...)
}

// A changed draft must never appear to describe an older retained recording.
func (m *audioWorkbench) workspaceRecordingRows() []string {
	if m.result == nil {
		return nil
	}
	draft, recorded := m.effectiveSettings(), m.result.entry.Settings
	draft.Preview, recorded.Preview = false, false
	if draft == recorded {
		return nil
	}
	rows := []string{"  " + m.warningStyle().Render("Draft changed · Showing previous audio.")}
	if recorded.Input == draft.Input {
		return rows
	}
	input := strings.Join(strings.Fields(readable.Text(m.result.entry.Settings.Input)), " ")
	if input == "" {
		input = "Generated speech"
	}
	return append(rows, "  "+m.muted().Render("Recording: ")+ansi.Truncate(input, max(1, m.audioWidth()-15), "…"))
}

func (r *audioWorkResult) hasWaveform() bool {
	return r.seekDuration() > 0 && len(r.analysis.Peaks) > 0
}

func (m *audioWorkbench) workspaceAudioHeader() []string {
	label := "Audio"
	left := "  " + m.muted().Render(label) + strings.Repeat(" ", m.formLabelWidth()+1-len(label))
	r := m.result
	if r == nil {
		return []string{left + m.emptyAudioStatus()}
	}
	status := m.playerStatus()
	end := 2 + 2*audioWaveformBars(m.waveformWidth()) + 1
	width := end - ansi.StringWidth(left) - ansi.StringWidth(status) - 2
	cell := func(text string, limit int) string {
		return ansi.Truncate(strings.Join(strings.Fields(readable.Text(text)), " "), limit, "…")
	}
	voice := cell(readable.ChoiceTitle(r.entry.Settings.Voice), 20)
	format, size := cell(strings.ToUpper(r.entry.Settings.Format), 8), audioFileSize(r.entry.Bytes)
	separator := " • "
	if ansi.StringWidth(strings.Join([]string{voice, format, size}, separator)) > width {
		separator = " "
	}
	suffix := separator + format + separator + size
	metadata := ansi.Truncate(voice, max(1, width-ansi.StringWidth(suffix)), "…") + suffix
	left += m.muted().Render(ansi.Truncate(metadata, width, "…"))
	lines := []string{left + strings.Repeat(" ", end-ansi.StringWidth(left)-ansi.StringWidth(status)) + status}
	lines = append(lines, m.workspaceRecordingRows()...)
	if !r.hasWaveform() {
		lines = append(lines, "  "+m.muted().Render("Preview unavailable"))
	}
	return lines
}

// The budget includes the lower seek rail. Empty and unavailable players have neither.
func (m *audioWorkbench) workspaceWaveformRows(budget int) []string {
	if !m.result.hasWaveform() || budget < 2 {
		return nil
	}
	height := min(9, budget-1)
	if height%2 == 0 {
		height--
	}
	boundary := m.selectedBoundary()
	wave := audioDenseWaveform(m.result.analysis.Peaks, m.waveformWidth(), max(3, height), boundary)
	middle := len(wave) / 2
	// Connect the measured columns only at zero amplitude, keeping all peak tips.
	axis := []rune(waveformCenterline.Replace(wave[middle]))
	last := len(axis) - 1
	marker := 2 * boundary
	axis[0], axis[last] = '╶', '╴'
	switch marker {
	case 0:
		axis[0] = '┠'
	case last:
		axis[last] = '┨'
	}
	wave[middle] = string(axis)
	if height == 1 {
		wave = wave[middle : middle+1]
	}
	rows := m.workspaceHairlineRows(wave)
	for i, row := range rows {
		prefix := "  "
		if i == height/2 && m.focus == 4 && m.waveformFocused && !m.busy {
			prefix = m.accent().Render("› ")
		}
		rows[i] = prefix + row
	}
	rail := []rune(strings.Repeat("─", len(axis)))
	rail[0], rail[last] = '├', '┤'
	anchorStyle := m.primaryText()
	if marker == last && m.result.finished && !m.playing {
		anchorStyle = m.playbackProgressStyle()
	}
	border := m.borderStyle()
	return append(rows, "  "+border.Render(string(rail[:marker]))+anchorStyle.Render("┷")+border.Render(string(rail[marker+1:])))
}

func (m *audioWorkbench) workspacePlayerControls() []string {
	r := m.result
	play := "Play audio"
	if m.playing {
		play = "Stop audio"
	} else if r != nil && r.finished {
		play = "Replay audio"
	} else if duration := r.seekDuration(); r != nil && duration > 0 && r.playbackPosition() >= duration {
		play = "At end"
	}
	active := m.focus == 4 && !m.waveformFocused && !m.busy
	playRow := "  " + m.playbackActionRow(play, active && m.resultAction == 0)
	const saveWidth = 14
	saveRow := m.playerActionRow("Save audio…", active && m.resultAction == 1, saveWidth)
	columnWidth := m.formLabelWidth() + 1
	actions := playRow + strings.Repeat(" ", columnWidth-ansi.StringWidth(playRow)) + saveRow

	timing, timeWidth := m.playerTiming()
	end := 2 + 2*audioWaveformBars(m.waveformWidth()) + 1
	// The timer occupies the third grid column. Keep its right edge stable while seeking.
	timeEnd := min(2+2*columnWidth+timeWidth, end)
	var rows []string
	if timeEnd-timeWidth >= columnWidth+saveWidth+2 {
		gap := timeEnd - ansi.StringWidth(timing) - ansi.StringWidth(actions)
		rows = []string{actions + strings.Repeat(" ", gap) + timing}
	} else {
		rows = []string{strings.Repeat(" ", end-ansi.StringWidth(timing)) + timing, actions}
	}
	if r == nil || len(r.savedPaths) == 0 {
		return rows
	}
	label, path := "Saved: ", r.savedPaths[len(r.savedPaths)-1]
	pathWidth := max(0, m.audioWidth()-4-ansi.StringWidth(label))
	return append(rows, "  "+m.muted().Render(label+audioPathSuffix(path, pathWidth)))
}

// Zeroes are explicit empty-state placeholders; unavailable analysis stays unknown.
func (m *audioWorkbench) playerTiming() (string, int) {
	r := m.result
	if r == nil {
		return m.muted().Render("00:00.000 / 00:00.000"), 21
	}
	if !r.hasWaveform() {
		return m.muted().Render("--:--.--- / --:--.---"), 21
	}
	total := audioDuration(r.analysis.Duration)
	timing := m.muted().Render(audioDuration(r.playbackPosition().Seconds()) + " / " + total)
	// Reserve the widest elapsed value before playback or seeking changes it.
	return timing, max(21, 2*ansi.StringWidth(total)+3)
}
