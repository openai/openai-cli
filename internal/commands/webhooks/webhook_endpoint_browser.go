package webhooks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	sdkwebhooks "github.com/openai/openai-go/v3/webhooks"
	"github.com/urfave/cli/v3"
)

// Only the fields displayed by the browser enter its retained state.
type webhookEndpoint struct {
	id, name, url string
	events        []string
}

func discoverWebhookEndpoints(ctx context.Context, command *cli.Command) ([]webhookEndpoint, error) {
	state, _ := ctx.Value(webhookCommandKey{}).(webhookCommandContext)
	options, err := state.options.PrepareDiscovery(command.Root())
	if err != nil {
		return nil, err
	}
	client := openai.NewClient(state.options.DefaultRequestOptions(command)...)
	page, err := client.Webhooks.List(ctx, sdkwebhooks.WebhookListParams{}, options...)
	var endpoints []webhookEndpoint
	seen := map[string]bool{}
	for err == nil && page != nil {
		if err := ctx.Err(); err != nil {
			return endpoints, err
		}
		if len(page.Data) > 0 {
			cursor := page.Data[len(page.Data)-1].ID
			if cursor == "" || seen[cursor] {
				return endpoints, errors.New("missing or repeated pagination cursor")
			}
			seen[cursor] = true
		}
		for _, item := range page.Data {
			if item.ID == "" {
				return endpoints, errors.New("missing endpoint ID")
			}
			endpoints = append(endpoints, webhookEndpoint{item.ID, item.Name, item.URL, item.EventTypes})
		}
		if len(page.Data) == 0 {
			if page.HasMore {
				return endpoints, errors.New("empty pagination cursor")
			}
			break
		}
		if page.JSON.HasMore.Valid() && !page.HasMore {
			break
		}
		page, err = page.GetNextPage()
	}
	return endpoints, err
}

type webhookEndpointLoadMsg struct {
	generation uint64
	items      []webhookEndpoint
	err        error
}

type webhookEndpointBrowser struct {
	ctx              context.Context
	load             func(context.Context) ([]webhookEndpoint, error)
	cancel           context.CancelFunc
	generation       uint64
	active, loading  bool
	details, actions bool
	refresh          bool
	current, offset  int
	items            []webhookEndpoint
	note             string
	mu               sync.Mutex
	closed           bool
	workers          sync.WaitGroup
}

func (b *webhookEndpointBrowser) start() tea.Cmd {
	if b.cancel != nil {
		b.cancel()
	}
	ctx, cancel := context.WithCancel(b.ctx)
	b.cancel = cancel
	b.generation++
	generation := b.generation
	b.active, b.loading, b.details, b.actions = true, true, false, false
	b.current, b.offset, b.note = 0, 0, ""
	return func() tea.Msg {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return nil
		}
		b.workers.Add(1)
		b.mu.Unlock()
		defer b.workers.Done()
		items, err := b.load(ctx)
		return webhookEndpointLoadMsg{generation, items, err}
	}
}

func (b *webhookEndpointBrowser) close() {
	b.mu.Lock()
	b.closed = true
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
	b.workers.Wait()
}

func (b *webhookEndpointBrowser) back() {
	if b.details {
		b.details, b.offset = false, 0
		return
	}
	b.active = false
	b.generation++
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *webhookEndpointBrowser) receive(msg webhookEndpointLoadMsg) {
	if !b.active || msg.generation != b.generation {
		return
	}
	b.items, b.loading = msg.items, false
	b.current = min(b.current, max(0, len(b.items)-1))
	b.note = ""
	if msg.err != nil {
		b.note = "Could not load all webhooks. Check your connection, project, and webhook read access, then Refresh."
		var apiError *openai.Error
		if errors.As(msg.err, &apiError) {
			b.note = fmt.Sprintf("API error (HTTP %d). Check your key, project, and webhook read access, then Refresh.", apiError.StatusCode)
		}
	}
}

func (b *webhookEndpointBrowser) update(key string) tea.Cmd {
	if key == "esc" {
		b.back()
		return nil
	}
	if key == "tab" || key == "shift+tab" {
		b.actions, b.refresh = !b.actions, false
		return nil
	}
	if b.actions {
		switch key {
		case "left":
			b.refresh = false
		case "right":
			b.refresh = true
		case "up":
			b.actions = false
		case "enter":
			if b.refresh {
				return b.start()
			}
			b.back()
		}
		return nil
	}
	if b.loading {
		return nil
	}
	switch key {
	case "up":
		if b.details {
			b.offset = max(0, b.offset-1)
		} else {
			b.current = max(0, b.current-1)
		}
	case "down":
		if b.details {
			b.offset++
		} else if b.current >= len(b.items)-1 {
			b.actions = true
		} else {
			b.current++
		}
	case "enter":
		if !b.loading && len(b.items) > 0 {
			b.details, b.offset = true, 0
		}
	}
	return nil
}

func (m *webhookCreatePicker) endpointView(width, height int) tea.View {
	b := m.browser
	accent, muted, border := "#3159BC", "#657087", "#BAC4D8"
	if m.dark {
		accent, muted, border = "#8AA8FF", "#A4ACC2", "#51566B"
	}
	strong, quiet, label := lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle()
	rule := lipgloss.NewStyle()
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if m.color {
		strong = strong.Bold(true).Foreground(lipgloss.Color(accent))
		label = label.Foreground(lipgloss.Color(accent))
		quiet = quiet.Foreground(lipgloss.Color(muted))
		panel = panel.BorderForeground(lipgloss.Color(border))
		rule = rule.Foreground(lipgloss.Color(border))
	}
	inner, overhead := width-4, 5
	// Narrow footers wrap; omit framing in short windows to retain every control.
	compact := height < 14 || height < 16 && width < 68
	if compact {
		inner, overhead = width, 3
	}
	bindings := []string{"Tab actions", "Esc back"}
	if b.actions {
		content := "Tab list"
		if b.details {
			content = "Tab details"
		}
		bindings = []string{"←→ action", "Enter choose", content, "Esc back"}
	} else if b.details {
		bindings = []string{"↑↓ scroll", "Tab actions", "Esc back"}
	} else if !b.loading && len(b.items) > 0 {
		bindings = []string{"↑↓ move", "Enter details", "Tab actions", "Esc back"}
	}
	controls := webhookCreateControls(inner, label, quiet, bindings...)
	overhead += len(controls)
	table := !compact && !b.loading && !b.details && len(b.items) > 0
	if table {
		overhead += 2 // Column headings and their connected rule.
	}
	columns := !compact && !b.loading && len(b.items) > 0
	if columns && b.note != "" {
		overhead++ // End the columns before a full-width error note.
	}
	panelSplit := 0
	dividers := map[int]string{}
	var note []string
	if b.note != "" {
		note = strings.Split(ansi.Hardwrap(b.note, inner, false), "\n")
	}
	available := max(1, height-overhead-len(note))
	var rows []string
	if b.loading {
		rows = []string{"Loading webhooks..."}
	} else if b.details && len(b.items) > 0 {
		item := b.items[b.current]
		valueColumn, separator := 9, " "
		if !compact {
			valueColumn, panelSplit, separator = 11, 11, rule.Render(" │ ")
		}
		field := func(name string, values []string) {
			if len(values) == 0 {
				values = []string{""}
			}
			for _, value := range values {
				for _, line := range strings.Split(ansi.Hardwrap(webhookCreateLine(value), inner-valueColumn, false), "\n") {
					rows = append(rows, "  "+label.Render(fmt.Sprintf("%-6s", name))+separator+line)
					name = ""
				}
			}
		}
		field("Name", []string{item.name})
		field("ID", []string{item.id})
		field("URL", []string{item.url})
		field("Events", item.events)
		b.offset = min(b.offset, max(0, len(rows)-available))
		rows = rows[b.offset:min(len(rows), b.offset+available)]
	} else if len(b.items) == 0 {
		if b.note == "" {
			rows = []string{"No webhook endpoints in this project."}
		}
	} else {
		// Use display cells so Unicode names preserve the ID column.
		nameWidth := 0
		for _, item := range b.items {
			nameWidth = max(nameWidth, ansi.StringWidth(webhookCreateLine(item.name)))
		}
		nameWidth = min(max(8, nameWidth), max(8, inner/2-3))
		for index, item := range b.items {
			name := item.name
			if name == "" {
				name = item.id
			}
			name = ansi.Truncate(webhookCreateLine(name), nameWidth, "…")
			name += strings.Repeat(" ", max(0, nameWidth-ansi.StringWidth(name)))
			row := "  " + name
			if index == b.current && !b.actions {
				row = strong.Render("› " + name)
			}
			if table {
				row += rule.Render(" │ ") + quiet.Render(webhookCreateLine(item.id))
			} else {
				row += quiet.Render("  " + webhookCreateLine(item.id))
			}
			rows = append(rows, ansi.Truncate(row, inner, "…"))
		}
		b.offset = min(b.offset, max(0, len(rows)-available))
		if b.current < b.offset {
			b.offset = b.current
		} else if b.current >= b.offset+available {
			b.offset = b.current - available + 1
		}
		rows = rows[b.offset:min(len(rows), b.offset+available)]
		if table {
			panelSplit = nameWidth + 5
			header := "  " + strong.Render("Endpoint"+strings.Repeat(" ", nameWidth-8)) + rule.Render(" │ ") + strong.Render("ID")
			rows = append([]string{header, ""}, rows...)
			dividers[1] = "┼"
		}
	}
	if columns && len(note) > 0 {
		dividers[len(rows)] = "┴"
		rows = append(rows, "")
	}
	rows = append(rows, note...)
	back, refresh := "  [ Back ]", "  [ Refresh ]"
	if b.actions && !b.refresh {
		back = strong.Render("› [ Back ]")
		refresh = label.Render(refresh)
	} else if b.actions {
		back = label.Render(back)
		refresh = strong.Render("› [ Refresh ]")
	} else {
		back, refresh = label.Render(back), label.Render(refresh)
	}
	if !compact {
		dividers[len(rows)] = "─"
		if columns && len(note) == 0 {
			dividers[len(rows)] = "┴"
		}
	}
	rows = append(rows, "", back+"  "+refresh)
	for index := range rows {
		rows[index] = ansi.Truncate(rows[index], inner, "…")
	}
	heading := lipgloss.NewStyle().Bold(m.color)
	lines := []string{heading.Render("Existing webhooks")}
	if compact {
		lines = append(lines, rows...)
	} else {
		lines = webhookCreatePanel(rows, width, panelSplit, dividers, panel, rule, "Existing webhooks", heading)
	}
	lines = append(lines, controls...)
	return m.frame(lines, width, height)
}
