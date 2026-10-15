package custom

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

const audioInstructionsExample = "Speak warmly and clearly."
const audioCommandTitle = "Command · Bash/zsh"

type audioInformationState struct {
	title, text, parent string
	offset              int
}

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

func (m *audioWorkbench) openSave() tea.Cmd {
	if m.result == nil {
		return nil
	}
	m.modal, m.note = "save", ""
	m.path.SetValue("speech-" + m.result.entry.ID + "." + m.result.entry.Settings.Format)
	return m.path.Focus()
}

func (m *audioWorkbench) restoreEditorFocus() tea.Cmd {
	switch m.modal {
	case "save":
		return m.path.Focus()
	case "speed":
		return m.speedInput.Focus()
	case "":
		return m.focusSection(m.focus)
	}
	return nil
}

func (m *audioWorkbench) moreItems() []string {
	items := []string{"History", "Preview voice", "Clear form", "Show command", "Help"}
	if m.activeFailure() {
		items = append(items, "Last error")
	}
	return append(items, "Back to form")
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
	case "Show command":
		m.openInformation(audioCommandTitle, m.commandPreview(), "more")
	case "Last error":
		m.openRecovery()
	default:
		m.modal = ""
		m.focusSection(2)
		m.setting = 5
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
			m.result, m.priorResult = nil, nil
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
	if m.modal != "recovery" && m.modal != "error" {
		m.recoveryParent = m.modal
	} else if m.modal == "error" && m.failureParent != "recovery" {
		m.recoveryParent = m.failureParent
	}
	if m.recoveryParent == "info" && m.recoveryInfo == nil {
		m.recoveryInfo = &audioInformationState{m.infoTitle, m.infoText, m.infoParent, m.infoOffset}
	}
	m.modal, m.recoveryChoice = "recovery", 0
}

func (m *audioWorkbench) recoveryItems() []string {
	items := []string{}
	if m.failure.StatusCode == 401 || m.failure.StatusCode == 403 {
		items = append(items, "API setup")
	}
	return append(items, "Error details", "Back to form")
}

func (m *audioWorkbench) leaveRecovery() tea.Cmd {
	m.modal, m.recoveryParent = m.recoveryParent, ""
	m.lastFinished = time.Now()
	if m.modal == "info" && m.recoveryInfo != nil {
		m.infoTitle, m.infoText, m.infoParent = m.recoveryInfo.title, m.recoveryInfo.text, m.recoveryInfo.parent
		m.infoOffset = m.recoveryInfo.offset
	}
	m.recoveryInfo = nil
	if m.modal == "" {
		m.setting = 5
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
	parent := ""
	switch m.modal {
	case "recovery":
		parent = m.recoveryParent
	case "error":
		parent = m.failureParent
	case "info":
		if m.infoParent != "recovery" {
			return nil
		}
		parent = m.recoveryParent
	default:
		m.recoveryParent, m.failureParent = "", ""
		m.recoveryInfo = nil
		return nil
	}
	if parent == "recovery" || parent == "error" {
		parent = m.recoveryParent
	}
	if parent == "info" && m.infoParent == "recovery" {
		parent = m.recoveryParent
	}
	m.modal = parent
	if parent == "info" && m.recoveryInfo != nil {
		m.infoTitle, m.infoText, m.infoParent = m.recoveryInfo.title, m.recoveryInfo.text, m.recoveryInfo.parent
		m.infoOffset = m.recoveryInfo.offset
	}
	m.recoveryParent, m.failureParent = "", ""
	m.recoveryInfo = nil
	return m.restoreEditorFocus()
}

func (m *audioWorkbench) updateRecovery(key string) tea.Cmd {
	items := m.recoveryItems()
	m.recoveryChoice = audioMenuIndex(m.recoveryChoice, len(items), key)
	if key != "enter" {
		return nil
	}
	switch items[m.recoveryChoice] {
	case "API setup":
		m.openInformation("API setup", m.setupHelpText(), "recovery")
	case "Error details":
		m.failureParent, m.modal, m.failureOffset = "recovery", "error", 0
	case "Back to form":
		return m.leaveRecovery()
	}
	return nil
}

func (m *audioWorkbench) openInformation(title, text, parent string) {
	m.infoTitle, m.infoText, m.infoParent = title, text, parent
	m.infoOffset, m.modal = 0, "info"
}

func (m *audioWorkbench) setupHelpText() string {
	// Render the registered local guide into an owned buffer. Do not run the
	// command or replace shared writers while a playback worker can be active.
	root := m.command.Root()
	if help := root.Command("help"); help != nil {
		if setup := help.Command("setup"); setup != nil && setup.CustomHelpTemplate != "" {
			var buffer bytes.Buffer
			cli.HelpPrinter(&buffer, setup.CustomHelpTemplate, setup)
			return "Close this form before following these steps.\nThen reopen audio speak in that shell.\n\n" + buffer.String()
		}
	}
	return "Close this form and run:\n\n" + errorHelpInvocation(root) + " help setup\n\nThen reopen audio speak in the same shell."
}

func (m *audioWorkbench) infoRows() []string {
	if m.infoTitle == audioCommandTitle {
		return audioCommandRows(readable.Text(m.infoText), m.audioWidth())
	}
	return strings.Split(ansi.Wrap(readable.Text(m.infoText), m.audioWidth(), ""), "\n")
}

// Prose wrapping can discard spaces. Executable text keeps every sequence,
// using the existing picker decoder for linear, display-cell-aware wrapping.
func audioCommandRows(text string, width int) []string {
	var rows []string
	var line strings.Builder
	used := 0
	for len(text) > 0 {
		sequence, cells, read := imagePickerSequence(text)
		if used > 0 && used+cells > width {
			rows = append(rows, line.String())
			line.Reset()
			used = 0
		}
		line.WriteString(sequence)
		used += cells
		text = text[read:]
	}
	return append(rows, line.String())
}

func (m *audioWorkbench) informationHeight() int { return max(1, m.height-5) }

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
	footer := "↑↓ scroll · Esc back"
	if m.infoTitle == audioCommandTitle {
		if _, omit := fileReceiptRequestOptions(m.command.Root()); !omit {
			footer = "↑↓ · Ctrl+P print/exit · Esc back"
		}
	}
	lines = append(lines, "", m.muted().Render(footer))
	return tea.NewView(m.fitAudio(lines))
}

const audioFormHelp = `Use Tab to move between sections.
Use Up and Down to move through fields.
Enter chooses the highlighted action.

Enter in Text or Instructions creates speech.
Ctrl+G also creates speech.
Alt+Enter adds a new line.

Instructions start with an editable example.
Older models do not send Instructions.
Your instruction draft stays available when you change models.

History stores audio, Text, Instructions, and settings locally.
It stores only the inputs sent with each request.
Clear removes the current draft, not History or saved files.

Choose Play audio or Save audio in the result.
Space plays or stops; S opens Save.

Esc returns from a menu or result.
Esc from the form exits. Ctrl+C cancels and exits.`

func (m *audioWorkbench) progressText() string {
	action := map[string]string{"Working: generate": "Creating speech", "Working: history": "Opening History", "Working: open": "Opening audio", "Working: export": "Saving audio"}[m.note]
	if action == "" {
		action = "Working"
	}
	return fmt.Sprintf("%s… %.1fs · Ctrl+C cancels", action, time.Since(m.started).Seconds())
}
