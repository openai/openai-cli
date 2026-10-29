package batches

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
)

type batchBrowserChoice struct {
	value, title, detail, search string
	setting                      string // Current value displayed beside a guided form field.
	status, succeeded, failed    string
	batch                        gjson.Result
}

type batchBrowserScreen struct {
	kind, title, subtitle, chosen, notice string
	input                                 textinput.Model
	choices                               []batchBrowserChoice
	visible                               []int
	index, width, height                  int
	closed                                bool
	searchFocused                         bool
	actionsState                          *batchBrowserActionsState
	pathState                             *batchBrowserPathState
	document                              *batchBrowserDocument
	documentState                         *batchBrowserDocumentState
	documentRawAvailable, documentRaw     bool
	requestState                          *batchBrowserRequestState
	flow                                  *batchBrowserFlowState
	progress                              *batchBrowserProgress
}

func newBatchBrowserScreen(kind, title, subtitle string, choices []batchBrowserChoice) *batchBrowserScreen {
	input := textinput.New()
	input.SetStyles(textinput.DefaultStyles(terminal.DarkBackground(os.Getenv("COLORFGBG"))))
	input.Prompt = "Search  "
	input.Placeholder = "Filter this page"
	input.SetWidth(60)
	if kind == "list" || kind == "path" || kind == "requests" {
		input.Focus()
		input.SetVirtualCursor(false)
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
	m.title = fmt.Sprintf("%d batches", len(page.items))
	if len(page.items) == 1 {
		m.title = "1 batch"
	}
	m.choices = nil
	for _, batch := range page.items {
		id, status := batch.Get("id").Str, batch.Get("status").Str
		succeeded, failed := "—", "—"
		if completed, ok := batchCount(batch.Get("request_counts.completed")); ok {
			succeeded = fmt.Sprint(completed)
			if total, ok := batchCount(batch.Get("request_counts.total")); ok {
				succeeded += fmt.Sprintf("/%d", total)
			}
		}
		if count, ok := batchCount(batch.Get("request_counts.failed")); ok {
			failed = fmt.Sprint(count)
		}
		m.choices = append(m.choices, batchBrowserChoice{value: "batch:" + id, title: id,
			status: strings.ReplaceAll(status, "_", " "), succeeded: succeeded, failed: failed,
			detail: batch.Get("endpoint").Str, search: batchBrowserSearchText(batch), batch: batch})
	}
	if previous {
		m.choices = append(m.choices, batchBrowserChoice{value: "previous", title: "Previous page", detail: "Load the preceding page"})
	}
	if page.next != "" {
		m.choices = append(m.choices, batchBrowserChoice{value: "next", title: "Next page", detail: "Search another page of batches"})
	}
	m.filter()
	if len(m.visible) == 0 {
		m.searchFocused = true
	}
}

func batchBrowserSearchText(batch gjson.Result) string {
	var search strings.Builder
	for _, field := range []string{"id", "status", "endpoint"} {
		search.WriteString(batch.Get(field).String())
		search.WriteByte(' ')
	}
	search.WriteString(strings.ReplaceAll(batch.Get("status").Str, "_", " "))
	search.WriteByte(' ')
	batch.Get("metadata").ForEach(func(key, value gjson.Result) bool {
		search.WriteString(key.String())
		search.WriteByte(' ')
		search.WriteString(value.String())
		search.WriteByte(' ')
		return true
	})
	return strings.ToLower(search.String())
}

func BrowserStatus(batch gjson.Result) (string, string) {
	return batchBrowserStatusLabel(batch.Get("status").Str), batchBrowserStatusTone(batch)
}

func batchBrowserStatusTone(batch gjson.Result) string {
	switch batch.Get("status").Str {
	case "validating", "in_progress", "finalizing":
		return "active"
	case "failed", "expired":
		return "negative"
	case "cancelling", "cancelled":
		return "warning"
	case "completed":
		succeeded, succeededOK := batchCount(batch.Get("request_counts.completed"))
		failed, failedOK := batchCount(batch.Get("request_counts.failed"))
		total, totalOK := batchCount(batch.Get("request_counts.total"))
		if succeededOK && failedOK && totalOK && total > 0 && failed <= total && succeeded == total-failed {
			if failed > 0 {
				return "warning"
			}
			return "positive"
		}
	}
	return ""
}

func batchBrowserCountTone(batch gjson.Result, field string) string {
	count, valid := batchCount(batch.Get("request_counts." + field))
	if valid && count > 0 {
		if field == "completed" {
			return "positive"
		}
		if field == "failed" {
			return "negative"
		}
	}
	return ""
}

func batchBrowserStatusLabel(status string) string {
	switch status {
	case "validating":
		return "Validating"
	case "in_progress":
		return "In progress"
	case "finalizing":
		return "Finalizing"
	case "completed":
		return "Completed"
	case "failed":
		return "Failed"
	case "expired":
		return "Expired"
	case "cancelling":
		return "Cancelling"
	case "cancelled":
		return "Cancelled"
	case "":
		return "unknown"
	default:
		return status
	}
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

func (m *batchBrowserScreen) Init() tea.Cmd {
	m.closed = false
	if m.flow != nil {
		return m.initFlow()
	}
	if m.progress != nil {
		return m.startProgress()
	}
	if m.kind == "requests" {
		if m.requestState.loaded && !m.requestState.loading {
			if m.requestState.expanded {
				return m.loadRequestPreview()
			}
			return nil
		}
		return m.loadRequests(m.requestFirst(), false)
	}
	if m.kind == "document" {
		return m.loadDocument("anchor", 0)
	}
	if m.kind == "list" {
		return nil
	}
	return textinput.Blink
}
func (m *batchBrowserScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	defer func() {
		if m.closed {
			m.stopProgress()
		}
	}()
	if m.flow != nil {
		return m.updateFlow(message)
	}
	if command, handled := m.updateProgress(message); handled {
		return m, command
	}
	if m.kind == "requests" {
		return m.updateRequests(message)
	}
	if m.kind == "document" {
		return m.updateDocument(message)
	}
	if m.pathState != nil {
		return m.updatePath(message)
	}
	if m.actionsState != nil {
		return m.updateActions(message)
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(1, msg.Width-20))
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+n":
			if m.kind == "list" {
				m.chosen, m.closed = "create", true
				return m, tea.Quit
			}
		case "esc":
			m.chosen = "back"
			m.closed = true
			return m, tea.Quit
		case "ctrl+c":
			m.closed = true
			return m, tea.Quit
		case "ctrl+r":
			if m.kind == "list" {
				m.chosen = "refresh"
				m.closed = true
				return m, tea.Quit
			}
		case "up", "shift+tab":
			if m.kind == "list" && (m.searchFocused || m.index == 0) {
				m.searchFocused = true
				return m, nil
			}
			m.index = max(0, m.index-1)
			return m, nil
		case "down", "tab":
			if m.kind == "list" && m.searchFocused {
				if len(m.visible) > 0 {
					m.searchFocused = false
				}
				return m, nil
			}
			m.index = min(max(0, len(m.visible)-1), m.index+1)
			return m, nil
		case "enter":
			if m.kind == "return" {
				m.chosen = "back"
				m.closed = true
				return m, tea.Quit
			}
			if m.width < 30 || m.height < 7 {
				return m, nil
			}
			if m.kind == "list" && m.searchFocused && (len(m.visible) == 0 || !strings.HasPrefix(m.choices[m.visible[m.index]].value, "batch:")) {
				return m, nil
			}
			if len(m.visible) > 0 {
				m.chosen = m.choices[m.visible[m.index]].value
				m.closed = true
				return m, tea.Quit
			}
		}
	}
	if m.kind == "list" {
		previous := m.input.Value()
		position := m.input.Position()
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		if m.input.Value() != previous || m.input.Position() != position {
			m.searchFocused = true
		}
		if m.input.Value() != previous {
			m.filter()
			m.notice = ""
		}
		return m, command
	}
	return m, nil
}

func (m *batchBrowserScreen) View() tea.View {
	view := tea.NewView("")
	// Retain menu screen ownership through the final frame. Printed results
	// and their return prompt belong to the original terminal scrollback.
	view.AltScreen = m.kind != "return"
	if m.closed {
		return view
	}
	width, height := max(1, m.width), max(1, m.height)
	framed := view.AltScreen && width >= 50 && height >= 12
	if framed {
		width -= 2
		height -= 2
	}
	line := func(text string) string { return ansi.Truncate(batchInputLabel(text), width, "…") }
	muted, accent, divider := lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle()
	focus, secondary, edge := "#3159BC", "#657087", "#BAC4D8"
	fill, textColor := "#E9EFFE", "#20283B"
	positive, warning, negative := "#236B43", "#885B14", "#B23A3A"
	highlight := lipgloss.NewStyle()
	if terminal.DarkBackground(os.Getenv("COLORFGBG")) {
		focus, secondary, edge = "#8AA8FF", "#A4ACC2", "#51566B"
		fill, textColor = "#343D58", "#F2F4FA"
		positive, warning, negative = "#8DC9A0", "#E2BC79", "#E59A9A"
	}
	colors := os.Getenv("NO_COLOR") == "" && os.Getenv("FORCE_COLOR") != "0"
	if colors {
		accent = accent.Foreground(lipgloss.Color(focus))
		muted = muted.Foreground(lipgloss.Color(secondary))
		highlight = highlight.Foreground(lipgloss.Color(textColor)).Background(lipgloss.Color(fill))
		divider = divider.Foreground(lipgloss.Color(edge))
	}
	semantic := func(base lipgloss.Style, tone string) lipgloss.Style {
		if colors {
			switch tone {
			case "active":
				return base.Foreground(lipgloss.Color(focus))
			case "positive":
				return base.Foreground(lipgloss.Color(positive))
			case "warning":
				return base.Foreground(lipgloss.Color(warning))
			case "negative":
				return base.Foreground(lipgloss.Color(negative))
			}
		}
		return base
	}
	summaryValue := func(text, tone string) string { return semantic(muted, tone).Render(text) }
	finish := func(lines []string, cursor *tea.Cursor) tea.View {
		if framed {
			border := lipgloss.RoundedBorder()
			panel := []string{divider.Render(border.TopLeft + strings.Repeat(border.Top, width) + border.TopRight)}
			for _, text := range lines {
				padding := strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
				panel = append(panel, divider.Render(border.Left)+text+padding+divider.Render(border.Right))
			}
			panel = append(panel, divider.Render(border.BottomLeft+strings.Repeat(border.Bottom, width)+border.BottomRight))
			lines = panel
			if cursor != nil {
				cursor.Position.X++
				cursor.Position.Y++
			}
		}
		view.Content = strings.Join(lines, "\n")
		view.Cursor = cursor
		return view
	}
	escape := "back"
	if m.kind == "list" {
		escape = "exit"
	}
	shortcut := func(key, action string) string {
		return accent.Render(key) + " " + muted.Render(action)
	}
	keys := []string{shortcut("↑↓", "move"), shortcut("Enter", "open"), shortcut("Esc", escape)}
	if m.actionsState != nil {
		keys = m.actionKeys(shortcut)
	} else if m.kind == "list" {
		if m.searchFocused {
			keys[0] = shortcut("↓", "batches")
		}
		keys = []string{keys[0], keys[1], shortcut("Ctrl+R", "refresh"), keys[2]}
		if !(height < 10 && width < 70) {
			keys = append(keys[:2], shortcut("Ctrl+N", "create"), keys[2], keys[3])
		}
	}
	if m.kind == "document" {
		keys = []string{shortcut("↑↓", "scroll"), shortcut("PgUp/PgDn", "page"), shortcut("p", "print"), shortcut("Esc", "back")}
		if m.documentState.csvResults {
			keys[2] = shortcut("s", "save CSV")
			keys = append(keys[:3], shortcut("Ctrl+S", "save as"), keys[3])
		}
		if m.documentRawAvailable {
			label := "Raw JSON"
			if m.documentRaw {
				label = "Readable"
			}
			keys = append(keys[:3], shortcut("j", label), keys[3])
		}
	}
	if m.kind == "requests" {
		keys = m.requestKeys(shortcut)
	}
	if m.flow != nil {
		view.KeyboardEnhancements.ReportEventTypes = m.flow.submit
		keys = m.flowKeys(shortcut)
	}
	footer := []string{"  "}
	for _, key := range keys {
		last := len(footer) - 1
		gap := ""
		if footer[last] != "  " {
			gap = "  "
		}
		if ansi.StringWidth(footer[last]+gap+key) > width {
			footer = append(footer, "  "+key)
		} else {
			footer[last] += gap + key
		}
	}
	footer = append([]string{""}, footer...)
	if m.kind == "return" {
		var lines []string
		if m.title != "" {
			lines = append(lines, muted.Render(line(m.title)))
		}
		if height >= 4 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(ansi.Wrap(shortcut("Enter / Esc", "back"), width, ""), "\n")...)
		if width >= 30 {
			for i, text := range lines {
				if text != "" {
					lines[i] = "  " + ansi.Truncate(text, width-2, "…")
				}
			}
		}
		return finish(lines[:min(len(lines), height)], nil)
	}
	if m.width < 30 || m.height < 7 {
		return finish([]string{line("Resize to 30×7. Esc " + escape)}, nil)
	}
	contentWidth := width - 2
	if framed {
		contentWidth--
	}
	line = func(text string) string { return ansi.Truncate(batchInputLabel(text), contentWidth, "…") }
	sectionRule := func() string {
		return "  " + terminal.SectionDivider(contentWidth, colors, fill, edge, secondary)
	}
	if m.flow != nil {
		lines, cursor := m.flowLines(height, contentWidth, footer, sectionRule, muted, semantic(muted, "warning"), accent, highlight, colors)
		return finish(lines, cursor)
	}
	if m.kind == "document" {
		return finish(m.documentLines(height, contentWidth, footer, sectionRule, muted, accent, semantic, colors), nil)
	}
	if m.kind == "requests" {
		lines, cursor := m.requestLines(height, contentWidth, footer, sectionRule, muted, accent, highlight, semantic, colors)
		return finish(lines, cursor)
	}
	if m.actionsState != nil {
		lines, cursor := m.actionLines(width, height, contentWidth, footer, sectionRule, muted, accent, highlight, semantic, colors)
		return finish(lines, cursor)
	}
	succeededWidth, failedWidth, batchRows := 9, 6, 0
	for _, row := range m.choices {
		if strings.HasPrefix(row.value, "batch:") {
			succeededWidth = max(succeededWidth, len(row.succeeded))
			failedWidth = max(failedWidth, len(row.failed))
		}
	}
	for _, index := range m.visible {
		if strings.HasPrefix(m.choices[index].value, "batch:") {
			batchRows++
		}
	}
	idWidth := contentWidth - 19 - succeededWidth - failedWidth
	tableLayout := width >= 68 && height >= 10 && idWidth >= 16
	table := tableLayout && batchRows > 0
	var cursor *tea.Cursor
	var lines []string
	headerWidth := contentWidth
	count := line(m.title)
	searchWidth := headerWidth - ansi.StringWidth(count) - 2
	inline := searchWidth >= 24
	if !inline {
		searchWidth = contentWidth
	}
	query, x := m.searchView(searchWidth - 8)
	if m.input.Value() == "" {
		query = muted.Render(query)
	}
	search := "Search  " + query
	if m.searchFocused {
		cursor = tea.NewCursor(10+x, 0)
		cursor.Shape = tea.CursorBar
		cursor.Blink = true
	}
	if inline {
		search += strings.Repeat(" ", headerWidth-ansi.StringWidth(search)-ansi.StringWidth(count)) + muted.Bold(colors).Render(count)
		lines = append(lines, "  "+search)
	} else {
		lines = append(lines, "  "+search, "  "+strings.Repeat(" ", contentWidth-ansi.StringWidth(count))+muted.Bold(colors).Render(count))
	}
	if m.subtitle != "" {
		lines = append(lines, "  "+muted.Render(line(m.subtitle)))
	}
	if height >= 12 {
		lines = append(lines, sectionRule())
	}
	if batchRows == 0 {
		message := "No batches on this page."
		for _, choice := range m.choices {
			if strings.HasPrefix(choice.value, "batch:") {
				message = "No matches on this page."
				break
			}
		}
		lines = append(lines, "  "+muted.Render(line(message)))
	}
	cell := func(value string, size int, right bool) string {
		value = ansi.Truncate(batchInputLabel(value), size, "…")
		padding := strings.Repeat(" ", max(0, size-ansi.StringWidth(value)))
		if right {
			return padding + value
		}
		return value + padding
	}
	if table {
		lines = append(lines, muted.Render("  "+cell("Batch", idWidth, false)+"  "+cell("Status", 13, false)+"  "+cell("Succeeded", succeededWidth, true)+"  "+cell("Failed", failedWidth, true)))
	}
	var choice batchBrowserChoice
	if len(m.visible) > 0 {
		choice = m.choices[m.visible[m.index]]
	}
	batchSelected := strings.HasPrefix(choice.value, "batch:")
	gap := 0
	if height >= 10 {
		gap = 1
	}
	if batchSelected && height >= 14 {
		gap = 2
	}
	detailBudget := max(0, height-len(lines)-len(footer)-gap-1)
	detail := choice.detail
	if batchSelected {
		detail = "Endpoint: " + detail
	}
	details := strings.Split(ansi.Wrap(muted.Render(batchInputLabel(detail)), contentWidth, ""), "\n")
	if choice.value == "" || detail == "" {
		details = nil
	}
	if batchSelected && !table {
		counts := muted.Render("Failed ") + summaryValue(choice.failed, batchBrowserCountTone(choice.batch, "failed")) + muted.Render("  Succeeded ") + summaryValue(choice.succeeded, batchBrowserCountTone(choice.batch, "completed"))
		details = strings.Split(ansi.Wrap(counts, contentWidth, "")+"\n"+summaryValue(line(choice.status), batchBrowserStatusTone(choice.batch)), "\n")
	}
	rowIDWidth := contentWidth
	if table {
		rowIDWidth = idWidth
	}
	if batchSelected && ansi.StringWidth(batchInputLabel(choice.title)) > rowIDWidth {
		idLines := strings.Split(ansi.Wrap(muted.Render("ID: "+batchInputLabel(choice.title)), contentWidth, ""), "\n")
		if len(idLines)+len(details) <= detailBudget {
			details = append(idLines, details...)
		}
	}
	details = details[:min(len(details), detailBudget)]
	for i := range details {
		details[i] = "  " + ansi.Truncate(details[i], contentWidth, "…")
	}
	capacity := max(1, height-len(lines)-len(details)-len(footer)-gap)
	start := max(0, m.index-capacity+1)
	detailsShown, gapShown := false, false
	for i := start; i < len(m.visible) && i < start+capacity; i++ {
		row := m.choices[m.visible[i]]
		if i > start && !strings.HasPrefix(row.value, "batch:") && strings.HasPrefix(m.choices[m.visible[i-1]].value, "batch:") {
			if batchSelected {
				if gap > 1 {
					lines = append(lines, "")
				}
				lines = append(lines, details...)
				detailsShown = true
			}
			if gap > 0 {
				lines = append(lines, "")
				gapShown = true
			}
		}
		selected := i == m.index && !m.searchFocused
		base := lipgloss.NewStyle()
		if selected {
			base = highlight
		}
		name := ansi.Truncate(batchInputLabel(row.title), contentWidth, "…")
		suffix := ""
		if table && strings.HasPrefix(row.value, "batch:") {
			name = cell(row.title, idWidth, false)
			suffix = base.Render("  ") + semantic(base, batchBrowserStatusTone(row.batch)).Render(cell(row.status, 13, false)) +
				base.Render("  ") + semantic(base, batchBrowserCountTone(row.batch, "completed")).Render(cell(row.succeeded, succeededWidth, true)) +
				base.Render("  ") + semantic(base, batchBrowserCountTone(row.batch, "failed")).Render(cell(row.failed, failedWidth, true))
		}
		if selected {
			padding := strings.Repeat(" ", max(0, width-2-ansi.StringWidth(name+suffix)))
			label := highlight.Bold(colors)
			// Every styled segment retains the selected background, including gaps and padding.
			lines = append(lines, highlight.Render("> ")+label.Render(name)+suffix+highlight.Render(padding))
		} else {
			lines = append(lines, "  "+name+suffix)
		}

	}
	if !gapShown && gap > 0 {
		lines = append(lines, "")
	}
	if !detailsShown {
		lines = append(lines, details...)
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	return finish(lines, cursor)
}

// Search uses terminal-cell positions, including escaped text and complete graphemes.
func (m *batchBrowserScreen) searchView(width int) (string, int) {
	value := []rune(m.input.Value())
	position := len(batchInputLabel(string(value[:m.input.Position()])))
	text := batchInputLabel(string(value))
	boundary := 0
	for boundary < len(text) {
		_, _, size := terminal.Sequence(text[boundary:])
		if boundary+size > position {
			break
		}
		boundary += size
	}
	x, start := ansi.StringWidth(text[:boundary]), 0
	for x >= width && start < boundary {
		_, cells, size := terminal.Sequence(text[start:])
		x -= cells
		start += size
	}
	if text == "" {
		text = m.input.Placeholder
	}
	return ansi.Truncate(text[start:], width, ""), x
}

func runBatchBrowserScreen(ctx context.Context, screen *batchBrowserScreen) error {
	screen.chosen = ""
	return runBatchPickerModel(ctx, screen, batchBrowserInputPolicy())
}

func batchBrowserInputPolicy() batchPickerInputPolicy {
	return batchPickerInputPolicy{
		cancelled: "Batch browser interrupted. Existing batches keep running. If submission was interrupted, check batches before submitting again.",
		ended:     "Terminal input ended. Run batches browse again, or use batches list without a terminal.",
		overflow:  "Too much pending keyboard input. The browser accepts 64 queued events and 1 MiB of queued text. Retry with less input.",
	}
}
