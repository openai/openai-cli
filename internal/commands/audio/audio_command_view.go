package audio

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/readable"
	requestconfig "github.com/openai/openai-cli/internal/request"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

const audioFocusCommand = 3
const audioShellContinuation = "\\\n"

type audioCommandPart struct {
	text      string
	secondary bool
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
		{"printf %s ", true}, {audioShellContinuation, true},
		{"  " + audioShellQuote(string(data)), false}, {" | ", true}, {audioShellContinuation, true},
		{"  OPENAI_UNTRUSTED_STDIN=true ", true}, {m.invocation, false},
	}
	for _, arg := range requestArgs {
		parts = append(parts, audioCommandPart{" ", false}, audioCommandPart{audioShellContinuation, true}, audioCommandPart{"    " + audioShellQuote(arg), false})
	}
	return append(parts, audioCommandPart{" ", false}, audioCommandPart{audioShellContinuation, true},
		audioCommandPart{"    audio speak ", false}, audioCommandPart{audioShellContinuation, true},
		audioCommandPart{"      --output speech." + s.Format, false})
}

// Printing retains the exact protected-stdin command and invocation.
func (m *audioWorkbench) commandPreview() string {
	args, omit := requestconfig.NextCommandOptions(m.command.Root())
	if omit {
		return "Command preview unavailable with private request overrides."
	}
	var raw strings.Builder
	for _, part := range m.commandParts(m.effectiveSettings(), args) {
		raw.WriteString(part.text)
	}
	return raw.String()
}

func audioShellQuote(value string) string { return cliterminal.ShellQuote(value) }

// Cache request-derived wrapping across cursor blinks and unrelated redraws.
func (m *audioWorkbench) commandDisplayRows() []string {
	args, omit := requestconfig.NextCommandOptions(m.command.Root())
	if omit {
		m.commandCache = audioCommandCache{}
		return audioCommandRows("Command unavailable with private request overrides.", max(1, m.audioWidth()-4))
	}
	s := m.effectiveSettings()
	if m.speedEditing {
		value, valid := audioSpeedValue(m.speedInput.Value())
		if !valid {
			return audioCommandRows("Apply a valid Speed to preview the command.", max(1, m.audioWidth()-4))
		}
		s.Speed = value
	}
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
		continuation := part.secondary && part.text == audioShellContinuation
		if continuation {
			part.text = "\\"
		}
		text := readable.Text(part.text)
		var chunk strings.Builder
		flush := func() {
			text := chunk.String()
			if part.secondary {
				text = m.muted().Render(text)
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
	return m.focusSection(audioFocusCommand)
}

func (m *audioWorkbench) printCommand() tea.Cmd {
	if _, omit := requestconfig.NextCommandOptions(m.command.Root()); omit {
		return nil
	}
	m.commandToPrint = m.commandPreview()
	m.quit = true
	m.stopPlayback()
	return tea.Quit
}

func (m *audioWorkbench) commandFooter() []string {
	controls := [][2]string{{"↑↓", "scroll"}, {"PgUp/PgDn", "page"}}
	if _, omit := requestconfig.NextCommandOptions(m.command.Root()); !omit {
		controls = append(controls, [2]string{"Ctrl+P", "print/exit"})
	}
	return m.formShortcutRows(append(controls, [2]string{"Tab", "Audio"}, [2]string{"Shift+Tab", "More"}, [2]string{"Esc", "back"})...)
}

func (m *audioWorkbench) commandView() tea.View {
	footer := m.commandFooter()
	rows := m.commandDisplayRows()
	height := m.commandViewportHeight()
	start := audioScrollOffset(m.commandOffset, len(rows), height, "")
	m.commandOffset = start
	heading := audioCommandTitle
	if len(rows) > height {
		heading += fmt.Sprintf(" · %d–%d/%d", start+1, min(len(rows), start+height), len(rows))
	}
	input := strings.Join(strings.Fields(readable.Text(m.input.Value())), " ")
	title := m.strong()
	if m.framed() {
		title = m.primaryText()
	}
	lines := []string{"  " + title.Render("Text to speech"), "  " + ansi.Truncate(input, m.audioWidth()-4, "…"), "", "  " + m.muted().Render(heading)}
	for _, row := range rows[start:min(len(rows), start+height)] {
		lines = append(lines, "  "+row)
	}
	return tea.NewView(m.fitAudio(append(lines, footer...), len(footer)))
}

func (m *audioWorkbench) commandViewportHeight() int {
	return max(1, m.audioHeight()-4-len(m.commandFooter()))
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
