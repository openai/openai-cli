package audio

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *audioWorkbench) inlineActions() bool { return m.framed() && m.width >= 110 }

const audioColumnGap = 4

func (m *audioWorkbench) splitComposer() bool {
	return m.framed() && m.width >= 100
}

func (m *audioWorkbench) composerWidth() int {
	if m.splitComposer() {
		// Keep the script at a readable measure while leaving room for a dated model ID.
		return min(80, (m.audioWidth()-audioColumnGap)*11/20)
	}
	return m.audioWidth()
}

func (m *audioWorkbench) settingsWidth() int {
	if m.splitComposer() {
		return m.audioWidth() - m.composerWidth() - audioColumnGap
	}
	return m.audioWidth()
}

func (m *audioWorkbench) settingsLabelWidth() int {
	if m.splitComposer() {
		return 9
	}
	return m.formLabelWidth()
}

// The framed workspace keeps the draft and retained recording together.
func (m *audioWorkbench) workspaceView() tea.View {
	rows, cursor, _ := m.formRows(false)
	cursorRow := -1
	if cursor != nil {
		cursorRow = cursor.Y
	}
	rows = append(rows, "")
	rows = append(rows, m.formActionRows()...)
	rows = append(rows, "", m.listeningRule(), "")
	footer := m.workspaceFooter()
	// Most hints use one or two rows. Focus must not resize the player between them.
	footerRows := max(2, len(footer))
	header, controls := m.workspaceAudioHeader(), m.workspacePlayerControls()
	if m.modal == "seek" {
		field := m.primaryText().Render(audioSeekLabel) + m.editorView(m.seekInput)
		clocks := m.muted().Render("Current  " + audioSeekClock(m.result.playbackPosition()) + "  End  " + audioSeekClock(m.result.seekDuration()))
		if ansi.StringWidth(field)+2+ansi.StringWidth(clocks) <= m.audioWidth() {
			controls = []string{field + "  " + clocks}
		} else {
			controls = []string{field, "  " + clocks}
		}
	}
	controlRows := len(controls)
	waveRows := 0
	if m.result.hasWaveform() {
		waveRows = 2 // One measured row and the seek rail.
		if m.modal != "seek" {
			if len(m.result.savedPaths) == 0 {
				controlRows++ // Reserve the receipt before sizing the waveform.
			}
			// One form separator can yield to the receipt to preserve measured detail.
			capacity := m.audioHeight() - len(rows) - len(header) - controlRows - footerRows
			waveRows = m.waveformDetailHeight(capacity) + 1
		}
	}
	contentRows := 0
	for _, row := range rows {
		if row != "" {
			contentRows++
		}
	}
	gapBudget := max(0, m.audioHeight()-contentRows-footerRows-len(header)-controlRows-waveRows)
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
	budget := m.audioHeight() - len(lines) - len(header) - controlRows - footerRows
	wave := m.workspaceWaveformRows(budget)
	free := budget - len(wave)
	if len(wave) > 0 && free == 0 && len(lines) > 0 && lines[len(lines)-1] == "" {
		// On short screens, move the divider's spare row below the metadata.
		lines = lines[:len(lines)-1]
		free++
	}
	lines = append(lines, header...)
	if len(wave) > 0 && free > 0 {
		lines = append(lines, "")
		free--
	}
	if m.height >= 36 && free > 0 {
		lines = append([]string{""}, lines...)
		if cursor != nil {
			cursor.Y++
		}
	}
	lines = append(lines, wave...)
	if m.modal == "seek" {
		cursor = audioEditorCursor(m.seekInput, len(audioSeekLabel), len(lines))
	}
	lines = append(lines, controls...)
	return m.cursorView(append(lines, footer...), len(footer), cursor)
}

func (m *audioWorkbench) workspaceFooter() []string {
	if m.busy {
		return m.formShortcutRows([2]string{"Ctrl+C", "cancel"})
	}
	if m.modal == "seek" {
		hint := "  " + m.muted().Render("Enter seconds or M:SS, for example 2.5 or 1:02.500.")
		if m.seekError != "" {
			hint = "  " + m.warningStyle().Render(m.seekError)
		}
		return []string{hint, m.shortcutLine([2]string{"Enter", "apply"}, [2]string{"Esc", "cancel"})}
	}
	if m.focus == 4 {
		return m.resultHintRows()
	}
	below := "Audio"
	if m.result.hasWaveform() {
		below = "waveform"
	}
	if m.inlineActions() && m.modal == "more" {
		return m.formShortcutRows([2]string{"←→", "choose"}, [2]string{"↑", "Settings"}, [2]string{"↓", below}, [2]string{"Enter", "open"}, [2]string{"Esc", "back"})
	}
	nextKey, next := "Tab", "Settings"
	if m.focus == 0 && m.instructionsSupported() {
		next = "Instructions"
	} else if m.focus == 2 {
		next = "Audio"
		if m.inlineActions() {
			next = "Actions"
			if m.setting == 4 || m.setting == audioSettingDisclosure {
				nextKey = "→"
			}
		}
	}
	shortcuts := [][2]string{{"Ctrl+C", "Exit"}, {nextKey, next}}
	if m.focus == 0 || m.focus == 1 {
		shortcuts = append(shortcuts, [2]string{"Alt+Enter", "New line"})
	} else if m.focus == 2 && m.setting >= 4 && m.setting <= audioSettingMore {
		horizontal := m.inlineActions() || len(m.formActionRows()) == 1
		if horizontal && !m.inlineActions() {
			shortcuts = append(shortcuts, [2]string{"←→", "choose"})
		}
		if horizontal || m.setting == audioSettingMore {
			shortcuts = append(shortcuts, [2]string{"↓", below})
		}
		action := "open"
		if m.setting == 4 {
			action = "generate"
		}
		shortcuts = append(shortcuts, [2]string{"Enter", action})
	} else if m.focus == 2 && m.setting == audioSettingDisclosure {
		action := "expand"
		if m.settingsExpanded {
			action = "collapse"
		}
		shortcuts = append(shortcuts, [2]string{"Enter", action})
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
	if m.focus == 2 && m.settingsExpanded && audioAdvancedSetting(m.setting) {
		shortcuts = append(shortcuts, [2]string{"Esc", "Settings"})
	}
	shortcuts = append(shortcuts, [2]string{"Ctrl+S", "Save"})
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
	rows := []string{"  " + m.warningStyle().Render("Draft changed. Showing previous audio.")}
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

func (m *audioWorkbench) retainedGenerationErrorRow() string {
	if !m.retainedGenerationFailure || m.result == nil {
		return ""
	}
	hint := m.keyHint("F1", "details")
	width := m.audioWidth() - ansi.StringWidth(hint) - 6
	summary := ansi.Truncate(readable.Text(m.failure.Summary), width, "…")
	return "  " + m.failureStyle().Render("! "+summary) + "  " + hint
}

func (m *audioWorkbench) workspaceAudioHeader() []string {
	r := m.result
	if r == nil {
		if !m.framed() {
			return []string{"  " + m.emptyAudioStatus()}
		}
		state := m.muted().Render("No recording yet")
		if m.playerNotice != "" {
			state = m.warningStyle().Render(m.playerNotice)
		}
		return []string{"  " + m.muted().Render("Audio") + strings.Repeat(" ", m.formLabelWidth()+1-len("Audio")) + state}
	}
	status := m.playerStatus()
	end := 2 + m.waveformWidth()
	left := "  "
	if m.framed() {
		left += m.muted().Render("Audio") + strings.Repeat(" ", m.formLabelWidth()+1-len("Audio"))
	}
	width := end - ansi.StringWidth(left) - ansi.StringWidth(status) - 1
	cell := func(text string, limit int) string {
		return ansi.Truncate(strings.Join(strings.Fields(readable.Text(text)), " "), limit, "…")
	}
	voice := cell(readable.ChoiceTitle(r.entry.Settings.Voice), 20)
	format, size := cell(strings.ToUpper(r.entry.Settings.Format), 8), audioFileSize(r.entry.Bytes)
	separator := "  "
	if ansi.StringWidth(strings.Join([]string{voice, format, size}, separator)) > width {
		separator = " "
	}
	suffix := separator + format + separator + size
	voice = ansi.Truncate(voice, max(1, width-ansi.StringWidth(suffix)), "…")
	left += ansi.Truncate(m.primaryText().Render(voice)+m.muted().Render(suffix), width, "…")
	lines := []string{left + strings.Repeat(" ", end-ansi.StringWidth(left)-ansi.StringWidth(status)) + status}
	if issue := m.retainedGenerationErrorRow(); issue != "" {
		lines = append(lines, issue)
	}
	lines = append(lines, m.workspaceRecordingRows()...)
	if !r.hasWaveform() {
		lines = append(lines, "  "+m.muted().Render("Preview unavailable"))
	}
	return lines
}

func (m *audioWorkbench) waveformDetailHeight(capacity int) int {
	height := min(audioMaxWaveformHeight, max(9, m.height-23), max(1, capacity))
	if height%2 == 0 {
		height--
	}
	return height
}

// The budget includes the lower seek rail. Empty and unavailable players have neither.
func (m *audioWorkbench) workspaceWaveformRows(budget int) []string {
	if !m.result.hasWaveform() || budget < 2 {
		return nil
	}
	height := m.waveformDetailHeight(budget - 1)
	boundary := m.selectedBoundary()
	wave, levels := audioWaveformRows(m.result.analysis.Peaks, m.waveformWidth(), max(3, height), boundary, true)
	middle := len(wave) / 2
	// Connect the measured columns only at zero amplitude, keeping all peak tips.
	axis := []rune(waveformCenterline.Replace(wave[middle]))
	last := len(axis) - 1
	marker := audioWaveformMarker(len(axis), boundary)
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
	rows := m.workspaceHairlineRows(wave, marker, levels)
	for i, row := range rows {
		prefix := "  "
		if i == height/2 && m.focus == 4 && m.waveformFocused && !m.busy {
			prefix = m.accent().Render("› ")
		}
		rows[i] = prefix + row
	}
	// The heavier played rail remains distinct when the terminal disables color.
	played, remaining := "", ""
	if marker > 0 {
		played = m.elapsedWaveformStyle().Render("├" + strings.Repeat("━", marker-1))
	}
	if marker < last {
		remaining = m.borderStyle().Render(strings.Repeat("─", last-marker-1) + "┤")
	}
	anchorStyle := m.playheadStyle()
	if marker == last && m.result.finished && !m.playing {
		anchorStyle = m.playbackProgressStyle()
	}
	spare := budget - len(rows) - 1 // The rail needs one row.
	if spare > 0 {
		rows = append(rows, "")
	}
	rows = append(rows, "  "+played+anchorStyle.Render("┷")+remaining)
	// Keep both rail gaps and the metadata gap before spending a row on time labels.
	if spare > 3 && m.framed() {
		rows = append(rows, "  "+m.muted().Render(audioTimeRuler(last+1, m.result.seekDuration())))
	}
	if spare > 1 && m.framed() {
		rows = append(rows, "")
	}
	return rows
}

// Align the outer labels with the two endpoint cells and center the halfway label.
func audioTimeRuler(width int, duration time.Duration) string {
	label := func(at time.Duration) string {
		if duration >= time.Millisecond {
			at = at.Truncate(time.Millisecond)
		}
		return strings.TrimSuffix(strings.TrimRight(audioSeekClock(at), "0"), ".")
	}
	zero, mid, end := label(0), label(duration/2), label(duration)
	start := (width - len(mid)) / 2
	if mid == zero || start <= len(zero) || start+len(mid) >= width-len(end) {
		return zero + strings.Repeat(" ", max(1, width-len(zero)-len(end))) + end
	}
	return zero + strings.Repeat(" ", start-len(zero)) + mid + strings.Repeat(" ", width-start-len(mid)-len(end)) + end
}

func (m *audioWorkbench) playbackVerb() string {
	if m.result == nil {
		return "Play"
	}
	if m.playing {
		if m.result.resumable {
			return "Pause"
		}
		return "Stop"
	}
	if m.result.finished {
		return "Replay"
	}
	if m.result.resumable {
		return "Resume"
	}
	return "Play"
}

func (m *audioWorkbench) workspacePlayerControls() []string {
	r := m.result
	if r == nil && !m.framed() {
		return nil
	}
	play := m.playbackVerb() + " audio"
	if duration := r.seekDuration(); duration > 0 && !m.playing && !r.finished && r.playbackPosition() >= duration {
		play = "At end"
	}
	active := m.focus == 4 && !m.waveformFocused && !m.busy
	playRow := "  " + m.playbackActionRow(play, active && m.resultAction == 0)
	const saveWidth = 14
	saveRow := m.playerActionRow("Save audio…", active && m.resultAction == 1, saveWidth)
	columnWidth := audioFormLabelWidth + 1
	actions := playRow + strings.Repeat(" ", columnWidth-ansi.StringWidth(playRow)) + saveRow

	timing, timeWidth := m.playerTiming()
	end := 2 + m.waveformWidth()
	// The timer occupies the third grid column. Keep its right edge stable while seeking.
	timeEnd := 2 + 2*columnWidth + timeWidth
	var rows []string
	if timeEnd <= end {
		gap := timeEnd - ansi.StringWidth(timing) - ansi.StringWidth(actions)
		rows = []string{actions + strings.Repeat(" ", gap) + timing}
	} else {
		rows = []string{strings.Repeat(" ", max(0, end-ansi.StringWidth(timing))) + timing, actions}
	}
	if r == nil || len(r.savedPaths) == 0 {
		return rows
	}
	label, path := "Saved: ", r.savedPaths[len(r.savedPaths)-1]
	pathWidth := max(0, m.audioWidth()-4-ansi.StringWidth(label))
	return append(rows, "  "+m.muted().Render(label+audioPathSuffix(path, pathWidth)))
}

// A retained recording with unavailable analysis has no known duration.
func (m *audioWorkbench) playerTiming() (string, int) {
	r := m.result
	if r == nil {
		return m.muted().Render("00:00.000 / 00:00.000"), 21
	}
	if !r.hasWaveform() {
		return m.muted().Render("--:--.--- / --:--.---"), 21
	}
	total := audioDuration(r.analysis.Duration)
	elapsed := m.accent().Bold(false).Render(audioDuration(r.playbackPosition().Seconds()))
	timing := elapsed + m.borderStyle().Render(" / ") + m.muted().Render(total)
	// Reserve the widest elapsed value before playback or seeking changes it.
	return timing, max(21, 2*ansi.StringWidth(total)+3)
}
