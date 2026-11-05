package commandsuggest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// ErrCanceled means that the picker returned no selection.
var ErrCanceled = errors.New("command selection canceled")

type commandPicker struct {
	candidates      []Candidate
	matches         []int
	query           []rune
	cursor          int
	selected        int
	width, height   int
	result          string
	done            bool
	title           string
	integration     bool
	dark, color     bool
	backgroundKnown bool
}

func newCommandPicker(candidates []Candidate, title string, integration bool) *commandPicker {
	m := &commandPicker{candidates: candidates, title: title, integration: integration, dark: true}
	m.filter()
	return m
}

func (m *commandPicker) Init() tea.Cmd {
	if m.color {
		return tea.RequestBackgroundColor
	}
	return nil
}

func (m *commandPicker) filter() {
	m.matches = searchCandidates(m.candidates, string(m.query))
	m.selected = 0
}

func (m *commandPicker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.backgroundKnown = true
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.done = true
			return m, tea.Quit
		case "up", "ctrl+p":
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.selected+1 < len(m.matches) {
				m.selected++
			}
			return m, nil
		case "enter":
			if m.width < 40 || m.height < 12 || len(m.matches) == 0 {
				return m, nil
			}
			m.result = m.candidates[m.matches[m.selected]].Command
			m.done = true
			return m, tea.Quit
		}
		before := string(m.query)
		m.editQuery(msg)
		if before != string(m.query) {
			m.filter()
		}
		return m, nil
	case tea.PasteMsg:
		before := string(m.query)
		m.insertQuery(msg.Content)
		if before != string(m.query) {
			m.filter()
		}
		return m, nil
	}
	return m, nil
}

func (m *commandPicker) editQuery(key tea.KeyPressMsg) {
	switch key.String() {
	case "left":
		m.cursor = max(0, m.cursor-1)
	case "right":
		m.cursor = min(len(m.query), m.cursor+1)
	case "home", "ctrl+a":
		m.cursor = 0
	case "end", "ctrl+e":
		m.cursor = len(m.query)
	case "ctrl+u":
		m.query, m.cursor = m.query[m.cursor:], 0
	case "ctrl+k":
		m.query = m.query[:m.cursor]
	case "backspace", "ctrl+h":
		if m.cursor > 0 {
			m.query = append(m.query[:m.cursor-1], m.query[m.cursor:]...)
			m.cursor--
		}
	case "delete", "ctrl+d":
		if m.cursor < len(m.query) {
			m.query = append(m.query[:m.cursor], m.query[m.cursor+1:]...)
		}
	default:
		if key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModSuper) == 0 {
			m.insertQuery(key.Text)
		}
	}
}

func (m *commandPicker) insertQuery(text string) {
	if !validQuery(text) {
		return
	}
	inserted := []rune(text)
	if len(inserted) > 256-len(m.query) {
		inserted = inserted[:256-len(m.query)]
	}
	draft := make([]rune, 0, len(m.query)+len(inserted))
	draft = append(draft, m.query[:m.cursor]...)
	draft = append(draft, inserted...)
	draft = append(draft, m.query[m.cursor:]...)
	m.query = draft
	m.cursor += len(inserted)
}

// Show a window of the query with a terminal-cell cursor, including wide text.
func (m *commandPicker) queryView(width int) (string, int) {
	available := max(1, width-8)
	start := 0
	for start < m.cursor && ansi.StringWidth(string(m.query[start:m.cursor])) >= available {
		start++
	}
	x := ansi.StringWidth(string(m.query[start:m.cursor]))
	text := ansi.Truncate(string(m.query[start:]), available, "…")
	if len(m.query) == 0 {
		text = ansi.Truncate("Type a command or description", available, "…")
	}
	return "Search  " + text, 8 + x
}

func validQuery(text string) bool {
	if len(text) > 4096 || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

// Match the filter's lowercase substrings without splitting graphemes or
// indexing original text with offsets from Unicode case conversion.
func highlightQuery(text string, words []string, selected bool) string {
	if len(words) == 0 || selected {
		return text
	}
	folded := strings.ToLower(text)
	matched := make([]bool, len(folded))
	for _, word := range words {
		for offset := 0; offset < len(folded); {
			index := strings.Index(folded[offset:], word)
			if index < 0 {
				break
			}
			start := offset + index
			for i := start; i < start+len(word); i++ {
				matched[i] = true
			}
			offset = start + 1
		}
	}
	on, off := ansi.NewStyle().Bold().String(), ansi.NewStyle().Normal().String()
	var out strings.Builder
	offset, active := 0, false
	for len(text) > 0 {
		cluster, _ := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
		end := offset + len(strings.ToLower(cluster))
		next := slices.Contains(matched[offset:end], true)
		if next != active {
			if next {
				out.WriteString(on)
			} else {
				out.WriteString(off)
			}
			active = next
		}
		out.WriteString(cluster)
		text, offset = text[len(cluster):], end
	}
	if active {
		out.WriteString(off)
	}
	return out.String()
}

func (m *commandPicker) View() tea.View {
	if m.done {
		// The inline renderer shrinks its cursor model before clearing. Keep
		// the search cursor's row in the final blank frame so clearing starts
		// at our origin, not at Search. Pick removes these rows after shutdown.
		return tea.NewView(strings.Repeat("\n", m.closingRows()-1))
	}
	if m.width < 40 || m.height < 12 {
		// Preserve the search cursor's row during a resize as well as on exit.
		warning := ansi.Truncate("Resize to at least 40 x 12. Esc cancels.", max(0, m.width-1), "…")
		return tea.NewView(warning + strings.Repeat("\n", m.closingRows()-1))
	}
	width := min(m.width-4, 88)
	framed := m.width >= 60 && m.height >= 16
	frameRows := 0
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if framed {
		width = min(m.width-8, 88)
		frameRows = 2
	}
	strong := lipgloss.NewStyle().Bold(true)
	muted, border, selected := lipgloss.NewStyle(), lipgloss.NewStyle(), strong
	control := lipgloss.NewStyle()
	if m.color && m.backgroundKnown {
		fill, accent, secondary, edge := "#E9EFFE", "#3159BC", "#657087", "#BAC4D8"
		if m.dark {
			fill, accent, secondary, edge = "#343D58", "#8AA8FF", "#A4ACC2", "#51566B"
		}
		selected = selected.Foreground(lipgloss.Color(accent)).Background(lipgloss.Color(fill))
		control = control.Foreground(lipgloss.Color(accent))
		muted = muted.Foreground(lipgloss.Color(secondary))
		border = border.Foreground(lipgloss.Color(edge))
		frame = frame.BorderForeground(lipgloss.Color(edge))
	}
	action := "selects"
	if m.integration {
		action = "fills prompt"
	}
	footer := []string{control.Render("↑↓") + " " + muted.Render("choose · ") + control.Render("Enter") + " " + muted.Render(action), control.Render("Esc") + " " + muted.Render("cancels")}
	if len(m.matches) == 0 {
		footer = []string{muted.Render("Edit your search · ") + control.Render("Esc") + " " + muted.Render("cancels")}
	} else if combined := strings.Join(footer, muted.Render(" · ")); ansi.StringWidth(combined) <= width {
		footer = []string{combined}
	}
	leadingRows, footerGapRows := 0, 1
	if m.height > 12 {
		leadingRows = 1
	}
	if framed {
		leadingRows, footerGapRows = 0, 0
	}
	rows := min(6, m.height-8-leadingRows-frameRows-footerGapRows-len(footer))
	start := max(0, m.selected-rows+1)
	count := fmt.Sprintf("%d commands", len(m.matches))
	if len(m.query) > 0 {
		count = fmt.Sprintf("%d matches", len(m.matches))
		if len(m.matches) == 1 {
			count = "1 match"
		}
	} else if len(m.matches) == 1 {
		count = "1 command"
	}
	title := ansi.Truncate(m.title, width-ansi.StringWidth(count)-1, "…")
	header := strong.Render(title) + strings.Repeat(" ", width-ansi.StringWidth(title)-ansi.StringWidth(count)) + muted.Render(count)
	queryLine, queryCursor := m.queryView(width)
	queryText := strings.TrimPrefix(queryLine, "Search  ")
	if len(m.query) == 0 {
		queryText = muted.Render(queryText)
	}
	queryLine = control.Render("Search") + "  " + queryText
	rule := border.Render(strings.Repeat("─", width))
	words := strings.Fields(strings.ToLower(string(m.query)))
	lines := make([]string, leadingRows)
	lines = append(lines, header, queryLine, rule)
	for i := 0; i < rows; i++ {
		index := start + i
		line := ""
		if index < len(m.matches) {
			candidate := m.candidates[m.matches[index]]
			label := ansi.Truncate(highlightQuery(candidate.Command, words, index == m.selected), width-2, "…")
			line = "  " + label
			if index == m.selected {
				line = "› " + label
				line = selected.Render(line + strings.Repeat(" ", width-ansi.StringWidth(line)))
			}
		} else if len(m.matches) == 0 && i == 0 {
			line = muted.Render("  No matching commands")
		}
		lines = append(lines, line)
	}
	detailWidth := width - 2
	details := []string{"", "", ""}
	if len(m.matches) > 0 {
		candidate := m.candidates[m.matches[m.selected]]
		description := candidate.Description
		if description == "" {
			description = "Edit the command at your prompt before running it."
		}
		description = highlightQuery(description, words, false)
		wrapped := strings.Split(ansi.Wrap(description, detailWidth, ""), "\n")
		for i := 0; i < min(2, len(wrapped)); i++ {
			details[i] = wrapped[i]
		}
		if len(wrapped) > 2 {
			details[1] = ansi.Truncate(wrapped[1], detailWidth-1, "") + "…"
		}
		if ansi.StringWidth(candidate.Command) > width-2 {
			details[0] = ansi.Truncate(description, detailWidth, "…")
			preview := strings.Split(ansi.Hardwrap(highlightQuery(candidate.Command, words, false), detailWidth, false), "\n")
			for i := 0; i < min(2, len(preview)); i++ {
				details[i+1] = preview[i]
			}
			if len(preview) > 2 {
				details[2] = ansi.Truncate(preview[1], detailWidth-1, "") + "…"
			}
		}
	} else {
		details[0] = "Try a broader search."
	}
	for len(details) > 1 && details[len(details)-1] == "" {
		details = details[:len(details)-1]
	}
	if len(words) > 0 {
		// A wrapped match can continue into omitted rows. End its emphasis
		// before rendering the footer, even when the preview is truncated.
		details[len(details)-1] += ansi.NewStyle().Normal().String()
	}
	carryBold := false
	for i := range details {
		if framed {
			// Detail highlights use only bold and normal intensity. Close each
			// row before its border, then reopen continued matches on the next.
			on, off := ansi.NewStyle().Bold().String(), ansi.NewStyle().Normal().String()
			if carryBold {
				details[i] = on + details[i]
			}
			carryBold = strings.LastIndex(details[i], on) > strings.LastIndex(details[i], off)
			details[i] += off
		}
		details[i] = "  " + details[i]
	}
	lines = append(lines, rule)
	lines = append(lines, details...)
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	cursorX, cursorY := queryCursor+2, 1+leadingRows
	if framed {
		body := frame.Render(strings.Join(lines[leadingRows:], "\n"))
		lines = append(make([]string, leadingRows), strings.Split(body, "\n")...)
		cursorX += 2
		cursorY++
	}
	if footerGapRows > 0 {
		lines = append(lines, "")
	}
	for _, hint := range footer {
		if framed {
			hint = "  " + hint
		}
		lines = append(lines, hint)
	}
	for i, line := range lines {
		lines[i] = "  " + line
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.Cursor = tea.NewCursor(cursorX, cursorY)
	view.Cursor.Shape = tea.CursorBar
	return view
}

func (m *commandPicker) closingRows() int {
	return min(3, max(1, m.height))
}

// Pick runs the shared local selector. It returns data and never executes it.
// Input and output belong to the foreground terminal, separate from data pipes.
func Pick(ctx context.Context, input, output *os.File, candidates []Candidate, title string, integration bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) || os.Getenv("TERM") == "dumb" {
		return "", errors.New("command search requires a terminal with cursor support")
	}
	if len(candidates) == 0 {
		return "", errors.New("no commands to explore")
	}
	for _, candidate := range candidates {
		if !validCommand(candidate.Command) {
			return "", errors.New("invalid command suggestions")
		}
	}
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return "", errors.New("could not read terminal size")
	}
	m := newCommandPicker(candidates, title, integration)
	m.width, m.height = width, height
	m.color = os.Getenv("NO_COLOR") == "" && colorprofile.Detect(output, os.Environ()) >= colorprofile.ANSI256
	programContext, cancel := context.WithCancel(ctx)
	defer cancel()
	tracked := &pickerOutput{File: output, writer: output, cancel: cancel}
	program := tea.NewProgram(m, tea.WithInput(input), tea.WithOutput(tracked), tea.WithContext(programContext), tea.WithWindowSize(width, height))
	_, err = program.Run()
	if err == nil && m.done {
		// Graceful shutdown leaves the cursor at the bottom of the final
		// blank frame. Return to its origin without clearing shell history.
		cleanup := "\r"
		if rows := m.closingRows(); rows > 1 {
			cleanup += ansi.CursorUp(rows - 1)
		}
		_, _ = io.WriteString(tracked, cleanup+ansi.EraseScreenBelow)
	}
	if writeErr := tracked.failure(); writeErr != nil {
		return "", writeErr
	}
	if err != nil {
		return "", err
	}
	if m.result == "" {
		return "", ErrCanceled
	}
	return m.result, nil
}

// Bubble Tea does not propagate renderer write errors. Preserve those errors
// and stop reading input when the user cannot see the current selection.
type pickerOutput struct {
	*os.File
	writer io.Writer
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
}

func (output *pickerOutput) WriteString(text string) (int, error) { return output.Write([]byte(text)) }

func (output *pickerOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.err != nil {
		return 0, output.err
	}
	n, err := output.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		output.err = err
		output.cancel()
	}
	return n, err
}

func (output *pickerOutput) failure() error {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.err
}
