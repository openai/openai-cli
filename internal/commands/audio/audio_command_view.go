package audio

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/readable"
	requestconfig "github.com/openai/openai-cli/internal/request"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

const audioFocusCommand = 3
const audioShellContinuation = "\\\n"

type audioCommandRole uint8

const (
	commandValue audioCommandRole = iota
	commandSyntax
	commandOperation
)

type audioCommandPart struct {
	text string
	role audioCommandRole
}

type audioCommandCache struct {
	settings            audiohistory.Settings
	request, invocation string
	width               int
	color, dark, valid  bool
	rows                []string
}

func (m *audioWorkbench) commandParts(s audiohistory.Settings, requestArgs []string) []audioCommandPart {
	body := map[string]any{"model": s.Model, "voice": s.Voice, "response_format": s.Format, "speed": s.Speed, "input": s.Input}
	if s.Instructions != "" {
		body["instructions"] = s.Instructions
	}
	data, _ := json.Marshal(body)
	parts := []audioCommandPart{
		{"printf %s ", commandSyntax}, {audioShellContinuation, commandSyntax},
		{"  " + audioShellQuote(string(data)), commandValue}, {" | ", commandSyntax}, {audioShellContinuation, commandSyntax},
		{"  OPENAI_UNTRUSTED_STDIN=true ", commandSyntax}, {m.invocation, commandValue},
	}
	for _, arg := range requestArgs {
		parts = append(parts, audioCommandPart{" ", commandValue}, audioCommandPart{audioShellContinuation, commandSyntax}, audioCommandPart{"    " + audioShellQuote(arg), commandValue})
	}
	return append(parts, audioCommandPart{" ", commandValue}, audioCommandPart{audioShellContinuation, commandSyntax},
		audioCommandPart{"    audio speak ", commandOperation}, audioCommandPart{audioShellContinuation, commandSyntax},
		audioCommandPart{"      --output ", commandSyntax}, audioCommandPart{"speech." + s.Format, commandValue})
}

// The clipboard, printed command, and preview all use the same admission checks.
func (m *audioWorkbench) commandOptions() (args []string, speed float64, reason string) {
	args, omit := requestconfig.NextCommandOptions(m.command.Root())
	if omit {
		return nil, 0, "Command preview unavailable with private request overrides."
	}
	speed = m.settings.Speed
	if m.speedEditing {
		value, valid := audioSpeedValue(m.speedInput.Value())
		if !valid {
			return nil, 0, "Apply a valid Speed to preview the command."
		}
		speed = value
	}
	return args, speed, ""
}

func (m *audioWorkbench) rawCommand() (string, bool) {
	args, speed, reason := m.commandOptions()
	if reason != "" {
		return reason, false
	}
	var raw strings.Builder
	s := m.effectiveSettings()
	s.Speed = speed
	for _, part := range m.commandParts(s, args) {
		raw.WriteString(part.text)
	}
	return raw.String(), true
}

func audioShellQuote(value string) string { return cliterminal.ShellQuote(value) }

// Cache request-derived wrapping across cursor blinks and unrelated redraws.
func (m *audioWorkbench) commandDisplayRows() []string {
	args, speed, reason := m.commandOptions()
	if reason != "" {
		m.commandCache = audioCommandCache{}
		return audioCommandRows(reason, max(1, m.audioWidth()-4))
	}
	s := m.effectiveSettings()
	s.Speed = speed
	key, width := strings.Join(args, "\x00"), max(1, m.audioWidth()-4)
	c := &m.commandCache
	if c.valid && c.settings == s && c.request == key && c.invocation == m.invocation && c.width == width && c.color == m.color && c.dark == m.dark {
		return c.rows
	}
	if c.settings != s || c.request != key || c.invocation != m.invocation {
		m.commandOffset = 0
	}
	rows, used := []string{""}, 0
	for _, part := range m.commandParts(s, args) {
		continuation := part.role == commandSyntax && part.text == audioShellContinuation
		if continuation {
			part.text = "\\"
		}
		text := readable.Text(part.text)
		var chunk strings.Builder
		flush := func() {
			text := chunk.String()
			switch part.role {
			case commandSyntax:
				text = m.muted().Render(text)
			case commandOperation:
				text = m.accent().Render(text)
			}
			rows[len(rows)-1] += text
			chunk.Reset()
		}
		for len(text) > 0 {
			sequence, cells, consumed := cliterminal.Sequence(text)
			if used > 0 && used+cells > width {
				flush()
				rows = append(rows, "")
				used = 0
			}
			chunk.WriteString(sequence)
			used += cells
			text = text[consumed:]
		}
		flush()
		if continuation {
			rows = append(rows, "")
			used = 0
		}
	}
	*c = audioCommandCache{settings: s, request: key, invocation: m.invocation, width: width, color: m.color, dark: m.dark, valid: true, rows: rows}
	return rows
}

func (m *audioWorkbench) focusCommand() tea.Cmd {
	m.modal, m.setting, m.commandOffset = "", audioSettingMore, 0
	m.clipboard.notice = ""
	return m.focusSection(audioFocusCommand)
}

func (m *audioWorkbench) printCommand() tea.Cmd {
	raw, ok := m.rawCommand()
	if !ok {
		return nil
	}
	m.commandToPrint = raw
	m.quit = true
	m.stopPlayback()
	return tea.Quit
}

func (m *audioWorkbench) commandFooter() []string {
	var controls [][2]string
	if _, _, reason := m.commandOptions(); reason == "" {
		controls = append(controls, [2]string{"c", "copy"}, [2]string{"Ctrl+P", "print/exit"})
	}
	controls = append(controls, [2]string{"↑↓", "scroll"}, [2]string{"PgUp/PgDn", "page"}, [2]string{"Esc", "back"})
	return m.formShortcutRows(controls...)
}

func (m *audioWorkbench) commandView() tea.View {
	footer := m.commandFooter()
	rows := m.commandDisplayRows()
	height := m.commandViewportHeight()
	start := audioScrollOffset(m.commandOffset, len(rows), height, "")
	m.commandOffset = start
	heading := "  " + m.strong().Render(audioCommandTitle)
	if len(rows) > height {
		heading += m.muted().Render(fmt.Sprintf("  %d–%d/%d", start+1, min(len(rows), start+height), len(rows)))
	}
	var detail string
	if m.commandCache.valid {
		detail = "  " + m.muted().Render("Output") + "   " + m.primaryText().Render("speech."+m.commandCache.settings.Format)
	}
	if m.clipboard.notice != "" {
		detail = "  " + m.muted().Render(m.clipboard.notice)
	}
	lines := []string{heading, detail, m.listeningRule()}
	for _, row := range rows[start:min(len(rows), start+height)] {
		lines = append(lines, "  "+row)
	}
	return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
}

func (m *audioWorkbench) commandViewportHeight() int {
	return max(1, m.audioHeight()-3-len(m.commandFooter()))
}

// Executable wrapping keeps every sequence, including repeated whitespace.
func audioCommandRows(text string, width int) []string {
	var rows []string
	var line strings.Builder
	used := 0
	for len(text) > 0 {
		sequence, cells, read := cliterminal.Sequence(text)
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
