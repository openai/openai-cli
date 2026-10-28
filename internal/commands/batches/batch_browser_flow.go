package batches

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Guided screens reuse the browser's terminal session, frame, editor, and palette.
type batchBrowserFlowState struct {
	mode   string
	lines  []string
	offset int
	work   func() tea.Msg
}

func newBatchBrowserFlow(mode, title, subtitle string, choices []batchBrowserChoice) *batchBrowserScreen {
	m := newBatchBrowserScreen("flow", title, subtitle, choices)
	m.flow = &batchBrowserFlowState{mode: mode}
	if mode == "entry" || mode == "picker" {
		m.input.Focus()
		m.input.SetVirtualCursor(false)
		m.input.Placeholder = ""
	}
	return m
}

func (m *batchBrowserScreen) initFlow() tea.Cmd {
	if m.flow.work != nil {
		return m.flow.work
	}
	return nil
}

type batchBrowserWorkDone struct{ screen *batchBrowserScreen }

// The worker closes done before the controller reads its result, including on interruption.
func batchBrowserWork[T any](ctx context.Context, title string, show func(context.Context, *batchBrowserScreen) error, run func(context.Context) (T, error)) (T, error) {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newBatchBrowserFlow("busy", title, "Please wait. Ctrl+C interrupts this operation.", nil)
	var result T
	var workErr error
	done := make(chan struct{})
	go func() {
		result, workErr = run(workCtx)
		close(done)
	}()
	m.flow.work = func() tea.Msg {
		<-done
		return batchBrowserWorkDone{m}
	}
	err := show(ctx, m)
	if err != nil {
		cancel()
	}
	<-done
	if err != nil {
		return result, err
	}
	return result, workErr
}

func (m *batchBrowserScreen) updateFlow(message tea.Msg) (tea.Model, tea.Cmd) {
	s := m.flow
	if done, ok := message.(batchBrowserWorkDone); ok {
		if done.screen == m {
			m.closed, m.chosen = true, "done"
			return m, tea.Quit
		}
		return m, nil
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		return m, nil
	}
	if key, ok := message.(tea.KeyPressMsg); ok {
		if s.mode == "busy" {
			return m, nil
		}
		switch key.String() {
		case "esc", "ctrl+c":
			m.closed, m.chosen = true, "back"
			return m, tea.Quit
		case "ctrl+s":
			if s.mode == "confirm" && m.width >= 40 && m.height >= 12 {
				m.closed, m.chosen = true, "confirm"
				return m, tea.Quit
			}
		case "up", "shift+tab", "pgup":
			if s.mode == "confirm" {
				m.flow.offset = max(0, m.flow.offset-1)
				return m, nil
			}
			if s.mode != "entry" {
				m.index = max(0, m.index-1)
				return m, nil
			}
		case "down", "tab", "pgdown":
			if s.mode == "confirm" {
				m.flow.offset++
				return m, nil
			}
			if s.mode != "entry" {
				m.index = min(max(0, len(m.visible)-1), m.index+1)
				return m, nil
			}
		case "enter":
			if m.width < 30 || m.height < 7 || s.mode == "confirm" {
				return m, nil
			}
			if s.mode == "entry" {
				m.closed, m.chosen = true, "value"
				return m, tea.Quit
			}
			if len(m.visible) > 0 {
				m.closed, m.chosen = true, m.choices[m.visible[m.index]].value
				return m, tea.Quit
			}
		}
	}
	if s.mode == "entry" || s.mode == "picker" {
		previous := m.input.Value()
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		if s.mode == "picker" && m.input.Value() != previous {
			m.visible = nil
			query := strings.ToLower(m.input.Value())
			for i, choice := range m.choices {
				if choice.search == "" || strings.Contains(strings.ToLower(choice.search), query) {
					m.visible = append(m.visible, i)
				}
			}
			m.index = 0
		}
		return m, command
	}
	return m, nil
}

func (m *batchBrowserScreen) flowKeys(shortcut func(string, string) string) []string {
	switch m.flow.mode {
	case "entry":
		return []string{shortcut("Enter", "apply"), shortcut("Esc", "back")}
	case "confirm":
		return []string{shortcut("↑↓", "scroll"), shortcut("Ctrl+S", "confirm"), shortcut("Esc", "edit")}
	case "busy":
		return []string{shortcut("Ctrl+C", "interrupt")}
	default:
		return []string{shortcut("↑↓/Tab", "move"), shortcut("Enter", "choose"), shortcut("Esc", "back")}
	}
}

func (m *batchBrowserScreen) flowLines(height, width int, footer []string, rule func() string, muted, warning, accent, highlight lipgloss.Style) ([]string, *tea.Cursor) {
	lines := []string{"  " + accent.Bold(true).Render(ansi.Truncate(batchInputLabel(m.title), width, "…"))}
	wrap := func(text string) []string { return strings.Split(ansi.Wrap(batchInputLabel(text), width, ""), "\n") }
	headerBudget := max(0, height-len(footer)-4)
	var notices []string
	if m.notice != "" {
		notices = wrap(m.notice)
		notices = notices[:min(len(notices), headerBudget)]
	}
	if m.subtitle != "" {
		subtitle := wrap(m.subtitle)
		available := max(0, headerBudget-len(notices))
		if len(subtitle) > available {
			subtitle = subtitle[:available]
			if available > 0 {
				subtitle[available-1] = ansi.Truncate(subtitle[available-1]+"…", width, "…")
			}
		}
		for _, line := range subtitle {
			lines = append(lines, "  "+muted.Render(line))
		}
	}
	for _, line := range notices {
		lines = append(lines, "  "+warning.Render(line))
	}
	lines = append(lines, rule())
	var cursor *tea.Cursor
	if m.flow.mode == "entry" || m.flow.mode == "picker" {
		prompt := m.input.Prompt
		value, x := m.searchView(max(1, width-ansi.StringWidth(prompt)))
		cursor = tea.NewCursor(2+ansi.StringWidth(prompt)+x, len(lines))
		cursor.Shape, cursor.Blink = tea.CursorBar, true
		lines = append(lines, "  "+muted.Render(prompt)+value)
	}
	budget := max(0, height-len(lines)-len(footer))
	if m.flow.mode == "confirm" {
		var body []string
		for _, line := range m.flow.lines {
			body = append(body, wrap(line)...)
		}
		if m.width < 40 || m.height < 12 {
			body = wrap("Resize to 40×12 to review and confirm.")
		}
		m.flow.offset = min(m.flow.offset, max(0, len(body)-budget))
		for _, line := range body[m.flow.offset:min(len(body), m.flow.offset+budget)] {
			lines = append(lines, "  "+line)
		}
	} else {
		detail := ""
		if len(m.visible) > 0 {
			detail = m.choices[m.visible[m.index]].detail
		}
		var details []string
		if detail != "" && budget >= 4 {
			details = wrap(detail)
			details = details[:min(len(details), budget/2)]
		}
		rows := max(0, budget-len(details))
		start := max(0, m.index-rows+1)
		for i := start; i < len(m.visible) && i < start+rows; i++ {
			name := ansi.Truncate(batchInputLabel(m.choices[m.visible[i]].title), width, "…")
			if i == m.index {
				lines = append(lines, highlight.Render("> "+name+strings.Repeat(" ", max(0, width-ansi.StringWidth(name)))))
			} else {
				lines = append(lines, "  "+name)
			}
		}
		for _, line := range details {
			lines = append(lines, "  "+muted.Render(line))
		}
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	return lines[:min(height, len(lines))], cursor
}
