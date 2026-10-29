package audio

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/readable"
)

const audioInstructionsExample = "Speak warmly and clearly."
const audioCommandTitle = "Command (Bash/zsh)"
const audioNoGenerationNotice = "Generate speech first."

func (m *audioWorkbench) instructionsSupported() bool {
	return !strings.HasPrefix(m.settings.Model, "tts-1")
}

// The editor keeps its draft when the selected model cannot send Instructions.
func (m *audioWorkbench) effectiveSettings() audiohistory.Settings {
	settings := m.settings
	settings.Input = m.input.Value()
	settings.Instructions = ""
	if m.instructionsSupported() {
		settings.Instructions = m.instructions.Value()
	}
	return settings
}

func (m *audioWorkbench) noGenerationNotice() tea.Cmd {
	m.playerNotice = audioNoGenerationNotice
	return nil
}

func (m *audioWorkbench) openSave() tea.Cmd {
	if m.result == nil {
		return m.noGenerationNotice()
	}
	m.modal, m.note = "save", ""
	path, err := audioDefaultSavePath(m.result.entry)
	if err != nil {
		m.note = err.Error()
	}
	if path != "" {
		path = filepath.Base(path)
	}
	m.path.SetValue(path)
	m.saveFolder.SetValue(audioDefaultSaveFolder)
	m.resizeEditors()
	return m.focusSave(1)
}

func (m *audioWorkbench) focusSave(focus int) tea.Cmd {
	m.input.Blur()
	m.instructions.Blur()
	m.path.Blur()
	m.saveFolder.Blur()
	m.saveFocus = (focus + 3) % 3
	switch m.saveFocus {
	case 0:
		return m.path.Focus()
	case 1:
		return m.saveFolder.Focus()
	}
	return nil
}

func (m *audioWorkbench) updateSaveEditor(message tea.Msg) tea.Cmd {
	var command tea.Cmd
	switch m.saveFocus {
	case 0:
		m.path, command = m.path.Update(message)
	case 1:
		m.saveFolder, command = m.saveFolder.Update(message)
	}
	return command
}

func (m *audioWorkbench) updateSave(message tea.Msg, key string) tea.Cmd {
	switch key {
	case "tab":
		return m.focusSave(m.saveFocus + 1)
	case "shift+tab":
		return m.focusSave(m.saveFocus - 1)
	case "up", "down":
		if m.saveFocus == 2 {
			if key == "up" {
				return m.focusSave(1)
			}
			return nil
		}
		editor := &m.path
		if m.saveFocus == 1 {
			editor = &m.saveFolder
		}
		if !editor.HasSelection() {
			line := editor.LineInfo()
			if key == "up" && editor.Line() == 0 && line.RowOffset == 0 && m.saveFocus == 1 {
				return m.focusSave(0)
			}
			if key == "down" && editor.Line() == editor.LineCount()-1 {
				end := *editor
				end.CursorEnd()
				last := end.LineInfo()
				// Match the form editor's cursor-only wrap-row behavior.
				if line.RowOffset+1 == last.RowOffset && last.StartColumn == end.Column() {
					return m.updateSaveEditor(tea.KeyPressMsg{Code: tea.KeyEnd})
				}
				if line.RowOffset == line.Height-1 {
					return m.focusSave(m.saveFocus + 1)
				}
			}
		}
	case "enter":
		if m.saveFocus == 1 {
			if err := audioSaveFolderError(m.saveFolder.Value()); err != nil {
				m.note = err.Error()
				return nil
			}
			m.note = ""
			return m.focusSave(2)
		}
		filename, folder := m.path.Value(), m.saveFolder.Value()
		if err := audioSaveFilenameError(filename); err != nil {
			m.note = err.Error()
			return nil
		}
		if !audioSaveHasPath(filename) {
			if err := audioSaveFolderError(folder); err != nil {
				m.note = err.Error()
				return m.focusSave(1)
			}
		}
		if m.result == nil {
			return nil
		}
		m.modal = ""
		m.path.Blur()
		m.saveFolder.Blur()
		id, store := m.result.entry.ID, m.store
		return m.work("export", func(ctx context.Context) audioWorkMessage {
			path, useDefault, err := audioSaveDestination(filename, folder)
			if err != nil {
				return audioWorkMessage{kind: "export", err: audioFailureIf(err.Error(), err)}
			}
			snapshot, err := store.Open(ctx, id)
			if err != nil {
				return audioWorkMessage{kind: "export", err: classifiedAudioFailure(audioHistoryFailure, "Could not open retained audio for saving.", err)}
			}
			savedPath, err := saveWorkbenchAudio(ctx, snapshot, path, useDefault)
			err = errors.Join(err, snapshot.Close())
			return audioWorkMessage{kind: "export", path: savedPath, entryID: id, err: audioFailureIf("Could not save the original audio file.", err)}
		})
	}
	return m.updateSaveEditor(message)
}

func (m *audioWorkbench) restoreEditorFocus() tea.Cmd {
	switch m.modal {
	case "save":
		return m.focusSave(m.saveFocus)
	case "seek":
		return m.seekInput.Focus()
	case "speed":
		return m.speedInput.Focus()
	case "":
		if m.speedEditing {
			m.setting = 3
			m.focusSection(2)
			return m.speedInput.Focus()
		}
		return m.focusSection(m.focus)
	}
	return nil
}

func (m *audioWorkbench) moreItems() []string {
	items := []string{"History", "Preview voice", "Clear form", "View command", "Help"}
	if m.activeFailure() {
		items = append(items, "Last error")
	}
	return items
}

func (m *audioWorkbench) openActions() tea.Cmd {
	m.modal, m.setting, m.moreChoice = "more", audioSettingMore, 0
	return m.focusSection(2)
}

func (m *audioWorkbench) updateMore(key string) tea.Cmd {
	items := m.moreItems()
	m.moreChoice = audioMenuIndex(m.moreChoice, len(items), key)
	if key != "enter" {
		return nil
	}
	selected := items[m.moreChoice]
	switch selected {
	case "Help":
		m.openInformation("Help", audioFormHelp, "more")
	case "View command":
		return m.focusCommand()
	case "Last error":
		m.openRecovery()
	default:
		m.modal = ""
		m.focusSection(2)
		m.setting = audioSettingMore
		switch selected {
		case "History":
			return m.history()
		case "Preview voice":
			return m.generate(true)
		case "Clear form":
			m.stopPlayback()
			m.input.SetValue("")
			m.instructions.SetValue("")
			m.settings = audioDefaultSettings()
			m.settingsExpanded = false
			m.validation = nil
			m.retainedGenerationFailure = false
			m.result, m.priorResult = nil, nil
			m.playerNotice = ""
			m.note = "Draft cleared."
		}
		m.lastFinished = time.Now()
	}
	return nil
}

func audioMenuIndex(index, count int, key string) int {
	index = max(0, min(index, count-1))
	switch key {
	case "up":
		return max(0, index-1)
	case "down":
		return min(count-1, index+1)
	case "home":
		return 0
	case "end":
		return count - 1
	}
	return index
}

func (m *audioWorkbench) openRecovery() {
	if m.modal != "recovery" {
		m.recoveryParent = m.modal
	}
	m.modal, m.failureOffset = "recovery", 0
	m.recoveryRestoreFocus = false
}

func (m *audioWorkbench) leaveRecovery() tea.Cmd {
	m.modal, m.recoveryParent = m.recoveryParent, ""
	restoreFocus := m.recoveryRestoreFocus
	m.recoveryRestoreFocus = false
	m.lastFinished = time.Now()
	// Recovery must not strand an uncommitted Speed draft on another action.
	// Inspecting a previous failure also restores the previous section.
	if m.modal == "" && !restoreFocus && !m.speedEditing {
		m.setting = audioSettingMore
		return m.focusSection(2)
	}
	return m.restoreEditorFocus()
}

// A newer successful action owns the visible state. Do not leave an obsolete
// failure panel open after its error and status have been replaced.
func (m *audioWorkbench) reconcileRecovery() tea.Cmd {
	if m.quit || m.activeFailure() {
		return nil
	}
	m.recoveryRestoreFocus = false
	if m.modal == "recovery" {
		m.modal, m.recoveryParent = m.recoveryParent, ""
		return m.restoreEditorFocus()
	}
	m.recoveryParent = ""
	return nil
}

func (m *audioWorkbench) openInformation(title, text, parent string) {
	m.infoTitle, m.infoText, m.infoParent = title, text, parent
	m.infoOffset, m.modal = 0, "info"
}

func (m *audioWorkbench) infoRows() []string {
	return strings.Split(ansi.Wrap(readable.Text(m.infoText), m.audioWidth(), ""), "\n")
}

func (m *audioWorkbench) informationHeight() int {
	return max(1, m.audioHeight()-4)
}

func audioScrollOffset(offset, rows, height int, key string) int {
	limit := max(0, rows-height)
	offset = max(0, min(offset, limit))
	switch key {
	case "up":
		offset--
	case "down":
		offset++
	case "pgup":
		offset -= height
	case "pgdown":
		offset += height
	case "home":
		offset = 0
	case "end":
		offset = limit
	}
	return max(0, min(offset, limit))
}

func (m *audioWorkbench) informationView() tea.View {
	rows, height := m.infoRows(), m.informationHeight()
	start := audioScrollOffset(m.infoOffset, len(rows), height, "")
	lines := []string{m.strong().Render(m.infoTitle), ""}
	lines = append(lines, rows[start:min(len(rows), start+height)]...)
	footer := m.hintLine([2]string{"↑↓", "scroll"}, [2]string{"Esc", "back"})
	lines = append(lines, "", footer)
	return tea.NewView(m.fitAudio(lines, 1))
}

const audioFormHelp = `Use Tab to move between sections.
Use Up and Down to move through fields.
Enter chooses the highlighted action.

Enter in Text or Instructions creates speech.
Ctrl+G also creates speech.
Ctrl+S opens Save for the current recording. Choose a destination and confirm to write the file.
Ctrl+O focuses Audio without starting playback.
Text, settings, and retained audio share the workspace when the terminal is at least 60 x 24.
In smaller terminals, Tab changes the visible section.
From Settings, Tab focuses Actions on wide terminals. On smaller terminals, choose More actions.
Choose View command to inspect the full command.
Up/Down, Page Up/Down, Home, and End scroll the command.
Esc returns to the menu. Tab opens Audio. Shift+Tab returns to More actions.
Ctrl+P prints the full command and exits the form.
Voice stays on the form. Enter on Settings reveals Model, Format, and Speed.
Esc collapses Settings and keeps applied values.
Left and Right change the selected field.
Type a Speed directly; Enter applies it and Esc cancels the edit.
Alt+Enter adds a new line.

Instructions start with an editable example.
Older models do not send Instructions.
Your instruction draft stays available when you change models.

History stores audio, Text, Instructions, and settings locally.
It stores only the inputs sent with each request.
Clear removes the current draft, not History or saved files.

Choose Play audio or Save audio in the result.
Space plays the recording. Pause and Resume use the last reported position.
When that position is unavailable, Space stops playback. S opens Save.
Left and Right choose Play or Save.
Up focuses the waveform. Down returns to the selected action.
Left and Right on the waveform choose the playback start.
Shift+Left and Shift+Right move five waveform bars.
G or a digit opens Go to time for measured audio. Type seconds or M:SS.
Enter applies the time. Esc cancels. Invalid input restores the current position.
Changing the playback position stops playback. Press Space to play from the selected time.
The marker follows FFplay's reported playback position.
If FFplay does not report progress, the marker keeps its selected position.
Waveform height scales to each recording. Playback volume stays unchanged.
Save always exports the complete original recording.

Esc returns from a menu or result.
Esc from the form exits. Ctrl+C cancels and exits.`

func (m *audioWorkbench) progressView() string {
	action := map[string]string{"Working: generate": "Creating speech", "Working: history": "Opening History", "Working: open": "Opening audio", "Working: export": "Saving audio"}[m.note]
	if action == "" {
		action = "Working"
	}
	return m.muted().Render(fmt.Sprintf("%s… %.1fs  ", action, time.Since(m.started).Seconds())) + m.keyHint("Ctrl+C", "cancels")
}
