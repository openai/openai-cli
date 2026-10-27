package batches

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type batchBrowserChoice struct {
	value, title, detail, search, preview string
}

type batchBrowserScreen struct {
	kind, title, subtitle, chosen, notice string
	input                                 textinput.Model
	choices                               []batchBrowserChoice
	visible                               []int
	index, width, height                  int
}

func newBatchBrowserScreen(kind, title, subtitle string, choices []batchBrowserChoice) *batchBrowserScreen {
	input := textinput.New()
	input.SetStyles(textinput.DefaultStyles(terminal.DarkBackground(os.Getenv("COLORFGBG"))))
	input.Prompt = "Search this page: "
	input.SetWidth(60)
	if kind == "list" || kind == "path" {
		input.Focus()
	}
	m := &batchBrowserScreen{kind: kind, title: title, subtitle: subtitle, choices: choices, input: input, width: 80, height: 24}
	m.filter()
	return m
}

func newBatchBrowserList(page batchBrowsePage, previous bool) *batchBrowserScreen {
	m := newBatchBrowserScreen("list", "Batches", "", nil)
	m.setPage(page, previous)
	return m
}

func (m *batchBrowserScreen) setPage(page batchBrowsePage, previous bool) {
	m.subtitle = fmt.Sprintf("Search covers this page only: %d loaded batches.", len(page.items))
	m.choices = nil
	for _, batch := range page.items {
		id, status := batch.Get("id").Str, batch.Get("status").Str
		detail := status + "  " + batch.Get("endpoint").Str
		if completed, ok := batchCount(batch.Get("request_counts.completed")); ok {
			if total, ok := batchCount(batch.Get("request_counts.total")); ok {
				detail += fmt.Sprintf("  %d/%d succeeded", completed, total)
			}
		}
		m.choices = append(m.choices, batchBrowserChoice{value: "batch:" + id, title: id, detail: detail, search: batchBrowserSearchText(batch)})
	}
	if previous {
		m.choices = append(m.choices, batchBrowserChoice{value: "previous", title: "Previous page", detail: "Load the preceding page"})
	}
	if page.next != "" {
		m.choices = append(m.choices, batchBrowserChoice{value: "next", title: "Next page", detail: "Search another page of batches"})
	}
	m.choices = append(m.choices,
		batchBrowserChoice{value: "refresh", title: "Refresh", detail: "Reload the first page"},
		batchBrowserChoice{value: "quit", title: "Quit", detail: "Return to the shell"})
	m.filter()
}

func batchBrowserSearchText(batch gjson.Result) string {
	var search strings.Builder
	for _, field := range []string{"id", "status", "endpoint"} {
		search.WriteString(batch.Get(field).String())
		search.WriteByte(' ')
	}
	batch.Get("metadata").ForEach(func(key, value gjson.Result) bool {
		search.WriteString(key.String())
		search.WriteByte(' ')
		search.WriteString(value.String())
		search.WriteByte(' ')
		return true
	})
	return strings.ToLower(search.String())
}

func newBatchBrowserActions(id string, cmd *cli.Command) *batchBrowserScreen {
	invocation := clihelp.DisplayInvocation(cmd.Root())
	// Previews identify the command; inherited connection options stay private.
	prefix := invocation + " batches "
	arg := " --batch-id=" + terminal.ShellQuote(id)
	return newBatchBrowserScreen("actions", "Batch: "+id, "Copied patterns need the same connection and project options.", []batchBrowserChoice{
		{value: "status", title: "Status", detail: "Refresh counts, progress, and validation errors", preview: prefix + "retrieve" + arg},
		{value: "output", title: "Results", detail: "Read the returned answers", preview: prefix + "results" + arg},
		{value: "error", title: "Errors", detail: "Read failed requests, if an error file exists", preview: prefix + "results" + arg + " --file error"},
		{value: "input", title: "Inputs", detail: "Read the original request records", preview: prefix + "results" + arg + " --file input"},
		{value: "download", title: "Download", detail: "Save complete file bytes to a new path", preview: prefix + "download" + arg + " --file output --output NEW_PATH"},
		{value: "back", title: "Back", detail: "Choose another batch"},
	})
}

func newBatchBrowserDownload(id string) *batchBrowserScreen {
	return newBatchBrowserScreen("download", "Download: "+id, "Choose a file. The next screen asks for its destination.", []batchBrowserChoice{
		{value: "output", title: "Output file", detail: "Successful request records"},
		{value: "error", title: "Error file", detail: "Failed request records, when available"},
		{value: "input", title: "Input file", detail: "Original request records"},
		{value: "back", title: "Back", detail: "Return to batch actions"},
	})
}

func newBatchBrowserPath(id, selection string) *batchBrowserScreen {
	m := newBatchBrowserScreen("path", "Save "+selection+" file: "+id, "Enter a new file path. Existing files are never overwritten.", nil)
	m.input.Prompt = "New file path: "
	m.input.Placeholder = "./results.jsonl"
	return m
}

func newBatchBrowserReturn(title string) *batchBrowserScreen {
	return newBatchBrowserScreen("return", title, "", nil)
}

func (m *batchBrowserScreen) filter() {
	m.visible = m.visible[:0]
	search := strings.ToLower(m.input.Value())
	for i, choice := range m.choices {
		if m.kind != "list" || choice.search == "" || strings.Contains(choice.search, search) {
			m.visible = append(m.visible, i)
		}
	}
	m.index = 0
}

func (m *batchBrowserScreen) Init() tea.Cmd { return textinput.Blink }
func (m *batchBrowserScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(1, msg.Width-20))
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			m.chosen = "back"
			return m, tea.Quit
		case "ctrl+c":
			return m, tea.Quit
		case "up", "shift+tab":
			m.index = max(0, m.index-1)
			return m, nil
		case "down", "tab":
			m.index = min(max(0, len(m.visible)-1), m.index+1)
			return m, nil
		case "enter":
			if m.kind == "return" {
				m.chosen = "back"
				return m, tea.Quit
			}
			if m.width < 30 || m.height < 7 {
				return m, nil
			}
			if m.kind == "path" {
				value := m.input.Value()
				if strings.TrimSpace(value) == "" || value == "-" || os.IsPathSeparator(value[len(value)-1]) {
					m.notice = "Enter a new file path, not a directory or '-'."
					return m, nil
				}
				m.chosen = "save"
				return m, tea.Quit
			}
			if len(m.visible) > 0 {
				m.chosen = m.choices[m.visible[m.index]].value
				return m, tea.Quit
			}
		}
	}
	if m.kind == "list" || m.kind == "path" {
		previous := m.input.Value()
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		if m.input.Value() != previous {
			m.filter()
			m.notice = ""
		}
		return m, command
	}
	return m, nil
}

func (m *batchBrowserScreen) View() tea.View {
	line := func(text string) string { return ansi.Truncate(batchInputLabel(text), max(1, m.width), "…") }
	if m.kind == "return" {
		return tea.NewView(line(m.title) + "\n" + line("Enter or Esc: return to actions   Ctrl+C: quit"))
	}
	if m.width < 30 || m.height < 7 {
		return tea.NewView(line("Resize to 30×7. Esc: back   Ctrl+C: quit"))
	}
	var view strings.Builder
	view.WriteString(line(m.title) + "\n" + line(m.subtitle) + "\n")
	if m.kind == "list" || m.kind == "path" {
		view.WriteString(m.input.View() + "\n")
	} else {
		view.WriteString("\n")
	}
	if m.kind == "path" {
		view.WriteString(line(m.notice) + "\n" + line("Enter: download   Esc: back   Ctrl+C: quit"))
		return tea.NewView(view.String())
	}
	capacity := max(1, (m.height-5)/2)
	start := max(0, m.index-capacity+1)
	for i := start; i < len(m.visible) && i < start+capacity; i++ {
		choice := m.choices[m.visible[i]]
		prefix := "  "
		if i == m.index {
			prefix = "> "
		}
		view.WriteString(line(prefix+choice.title) + "\n" + line("  "+choice.detail) + "\n")
	}
	preview := ""
	if len(m.visible) > 0 {
		preview = m.choices[m.visible[m.index]].preview
	}
	if preview != "" {
		preview = "Pattern: " + preview
	}
	view.WriteString(line(preview) + "\n" + line("↑/↓ or Tab: choose   Enter: open   Esc: back   Ctrl+C: quit"))
	return tea.NewView(view.String())
}

func runBatchBrowserScreen(ctx context.Context, screen *batchBrowserScreen) error {
	screen.chosen = ""
	return runBatchPickerModel(ctx, screen, batchPickerInputPolicy{
		cancelled: "Batch browsing interrupted. The remote batches were not changed.",
		ended:     "Terminal input ended. Run batches browse again, or use batches list without a terminal.",
		overflow:  "Too much pending keyboard input. The browser accepts 64 queued events and 1 MiB of queued text. Retry with less input.",
	})
}
