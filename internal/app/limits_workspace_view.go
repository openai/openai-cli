package app

import (
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
)

func (m *limitsWorkspace) View() tea.View {
	view := tea.NewView("")
	if m.quitting {
		return view
	}
	if m.small() {
		view.Content = "Resize to at least 40 x 12.\nCtrl+C: quit"
		return view
	}
	width, height := m.contentWidth(), m.panelHeight()
	style := m.panelStyle()
	line := func(s string) string { return ansi.Truncate(limitsSafeLine(s), width, "…") }
	mark := func(s string, selected bool) string {
		text := ansi.Truncate(limitsSafeLine(s), width-2, "…")
		if selected {
			return style.selected.Render("› " + text)
		}
		return "  " + text
	}
	var body []string
	var inspectable bool
	footer := "Esc: back · Ctrl+C: quit"
	switch m.page {
	case "home":
		navigation, actions := "Tab to switch tabs · Up/Down to select", "Enter to open · q quit"
		if !m.framed() {
			actions += " · ? help"
		}
		footer = navigation + " · " + actions
		if ansi.StringWidth(footer) > width {
			footer = navigation + "\n" + actions
		}
		menu := m.menu()
		inspectable = m.selection < len(menu) && menu[m.selection].value.Exists()
		body = m.overviewRows(style, strings.Count(footer, "\n")+1, menu)
	case "form":
		body = append(body, style.muted.Render("Tab: next field · Shift+Tab: previous"))
		count := len(m.draft.inputs)
		var notice []string
		if m.formError != "" {
			first := limitsSafeLine(strings.SplitN(m.formError, "\n", 2)[0])
			notice = strings.Split(ansi.Wrap(first, width, ""), "\n")
			if len(notice) > 2 {
				notice = []string{notice[0], ansi.Truncate(strings.Join(notice[1:], " "), width, "…")}
			}
		}
		// Reserve navigation, Review, diagnostics, and the optional prefix action before fields.
		fieldRows := height - m.panelChrome() - 4 - len(notice)
		if count == 3 {
			fieldRows--
		}
		unavailable := m.draft.hasUnavailableEdits()
		if unavailable {
			fieldRows--
		}
		var fields []int
		position := 0
		for i := range m.draft.inputs {
			if m.draft.inputAvailable(i) {
				if i <= m.focus {
					position = len(fields)
				}
				fields = append(fields, i)
			}
		}
		if len(fields) < count {
			fieldRows--
		}
		visible := max(1, fieldRows/2)
		start := max(0, position-visible+1)
		end := min(len(fields), start+visible)
		for _, i := range fields[start:end] {
			m.draft.inputs[i].SetWidth(max(1, width-4))
			text := m.draft.labels[i]
			if m.draft.change.Kind == "rate_update" {
				text = limitsRateInputs[i].label + " (blank: unchanged)"
			}
			marker, label := "  ", style.muted.Render(line(text))
			if i == m.focus {
				marker = style.accent.Render("› ")
				label = style.selected.Render(line(text))
			}
			body = append(body, label, marker+ansi.Truncate(limitsInputView(m.draft.inputs[i]), width, "…"))
		}
		if len(fields) < count {
			body = append(body, line("Other rate fields were not returned."))
		}
		if count == 3 {
			modes := []string{"Keep unless edited", "Replace (empty is allowed)", "Clear to null"}
			body = append(body, mark("Prefix: "+modes[m.draft.prefixMode], m.focus == count))
		}
		if unavailable {
			body = append(body, mark("Discard unavailable edits", m.focus == count))
		}
		body = append(body, mark("Review change", m.focus >= count && !((count == 3 || unavailable) && m.focus == count)))
		body = append(body, notice...)
		footer = "Enter: next / select\nF1: help · Esc: back · Ctrl+C: quit"
		if m.refreshRequired {
			footer = "r: refresh · Enter: next field\nF1: help · Esc: back · Ctrl+C: quit"
		}
	case "review", "details":
		body = append(body, m.viewport.View())
		if m.page == "review" {
			back, apply := style.selected.Render("› Back"), "  Apply change"
			if m.apply {
				back, apply = "  Back", style.selected.Render("› Apply change")
			}
			if !m.viewport.AtBottom() {
				apply = "  Read to the end before applying"
			}
			body = append(body, ansi.Truncate(back+"   "+apply, width, "…"))
			footer = "↑↓ scroll · Tab action · Enter: select\nF1: help · Esc: back · Ctrl+C: quit"
			if m.previewCommand != "" {
				footer = "↑↓ scroll · Tab action · Enter: select\nCtrl+P: print command and exit\nF1: help · Esc: back · Ctrl+C: quit"
			}
		} else {
			footer = "↑↓: scroll · Esc: back · Ctrl+C: quit"
		}
	}
	if m.busy {
		status := "Loading…"
		if m.page == "review" {
			status = "Saving… Do not repeat this change."
		}
		footer = line(status) + "\nCtrl+C: interrupt"
	}
	// Keep the entire viewport and confirmation controls visible together.
	controls := strings.Split(footer, "\n")
	available := max(0, height-m.panelChrome()-len(controls))
	rows := strings.Split(strings.Join(body, "\n"), "\n")
	rows = rows[:min(len(rows), available)]
	if m.framed() {
		header := line(m.heading())
		if m.page == "home" {
			if !m.busy {
				tools := style.accent.Render("r") + " Refresh  " + style.accent.Render("?") + " Help"
				if inspectable {
					tools = style.accent.Render("i") + " Inspect  " + tools
				}
				header += strings.Repeat(" ", max(1, width-ansi.StringWidth(header)-ansi.StringWidth(tools))) + tools
			}
		} else {
			header += style.muted.Render(" · " + limitsSafeLine(m.scope()))
		}
		rows = append([]string{header, terminal.SectionDivider(width, m.color, style.fill, style.edge, style.secondary)}, rows...)
		for i, row := range rows {
			row = ansi.Truncate(row, width, "…")
			rows[i] = style.border.Render("│") + " " + row + strings.Repeat(" ", max(0, width-ansi.StringWidth(row))) + " " + style.border.Render("│")
		}
		rule := strings.Repeat("─", width+2)
		rows = append([]string{style.border.Render("╭" + rule + "╮")}, rows...)
		rows = append(rows, style.border.Render("╰"+rule+"╯"))
	} else {
		for i := range rows {
			rows[i] = ansi.Truncate(rows[i], width, "…")
		}
		rows = append([]string{line(m.heading() + " · " + m.scope())}, rows...)
		rows = append(rows, "")
	}
	for _, control := range controls {
		if !m.busy {
			shortcuts := strings.Split(control, " · ")
			for i, shortcut := range shortcuts {
				key, action, _ := strings.Cut(shortcut, " ")
				shortcuts[i] = style.accent.Render(key) + style.muted.Render(" "+action)
			}
			rows = append(rows, "  "+ansi.Truncate(strings.Join(shortcuts, style.muted.Render(" · ")), width, "…"))
		} else {
			rows = append(rows, "  "+style.muted.Render(line(control)))
		}
	}
	view.Content = strings.Join(rows, "\n")
	return view
}

type limitsPanelStyle struct {
	accent, selected, muted, border lipgloss.Style
	reported, cap, meter            lipgloss.Style
	fill, edge, secondary           string
}

func (m *limitsWorkspace) panelStyle() limitsPanelStyle {
	var s limitsPanelStyle
	if !m.color {
		return s
	}
	focus, ink := "#3159BC", "#20283B"
	s.fill, s.edge, s.secondary = "#E9EFFE", "#BAC4D8", "#657087"
	if m.dark {
		focus, ink = "#8AA8FF", "#F2F4FA"
		s.fill, s.edge, s.secondary = "#343D58", "#51566B", "#A4ACC2"
	}
	s.accent = s.accent.Foreground(lipgloss.Color(focus))
	s.selected = s.selected.Foreground(lipgloss.Color(ink)).Background(lipgloss.Color(s.fill)).Bold(true)
	reportedBG, reportedFG, capBG, capFG := "#C6ECE9", "#17615C", "#E2D9F7", "#61478B"
	if m.dark {
		reportedBG, reportedFG, capBG, capFG = "#285154", "#A1E5DF", "#563D79", "#DCC7FA"
	}
	s.meter = s.meter.Foreground(lipgloss.Color(reportedFG))
	s.reported = s.meter.Background(lipgloss.Color(reportedBG))
	s.cap = s.cap.Foreground(lipgloss.Color(capFG)).Background(lipgloss.Color(capBG))
	s.muted = s.muted.Foreground(lipgloss.Color(s.secondary))
	s.border = s.border.Foreground(lipgloss.Color(s.edge))
	return s
}

// Compare reported USD costs with the configured cap in cents, without rounding either amount.
func (m *limitsWorkspace) spendMeter(style limitsPanelStyle) (string, bool) {
	s := &m.snapshot
	if !s.CostsComplete || s.LimitAbsent || s.LimitUnconfirmed || !m.limitReadable() ||
		s.Limit.Get("currency").Str != "USD" || s.Limit.Get("interval").Str != "month" || limitsUniqueJSON(s.Limit) != nil {
		return "", false
	}
	capCents, err := limitsInteger(s.Limit.Get("threshold_amount"))
	cost, ok := new(big.Rat).SetString(s.CostsTotal)
	if err != nil || capCents <= 0 || !ok || cost.Sign() < 0 {
		return "", false
	}
	percentage := cost.Mul(cost, big.NewRat(10000, capCents))
	const cells = 16
	filled, label := cells, ">100%"
	if percentage.Cmp(big.NewRat(100, 1)) <= 0 {
		number := strings.TrimRight(strings.TrimRight(percentage.FloatString(2), "0"), ".")
		label = number + "%"
		rounded, _ := new(big.Rat).SetString(number)
		if rounded.Cmp(percentage) != 0 {
			label = "≈" + label
		}
		units := new(big.Rat).Mul(percentage, big.NewRat(cells, 100))
		filled = int(new(big.Int).Quo(units.Num(), units.Denom()).Int64())
	}
	bar := style.meter.Render(strings.Repeat("━", filled)) + style.border.Render(strings.Repeat("─", cells-filled))
	return style.muted.Render("Share of cap  ") + bar + "  " + style.meter.Render(label), true
}

func (m *limitsWorkspace) overviewRows(style limitsPanelStyle, footerRows int, menu []limitsMenuItem) []string {
	width := m.contentWidth()
	tabs := []string{"Spend", "Alerts"}
	if m.snapshot.ProjectID != "" {
		tabs = append(tabs, "Rates")
	}
	tabs[m.section] = style.accent.Bold(m.color).Render("[" + tabs[m.section] + "]")
	setting := func(label, value string) string {
		return "  " + style.muted.Render(label+strings.Repeat(" ", 7-len(label))) + value
	}
	period := "Current month · UTC"
	if m.snapshot.CostsStart != 0 {
		period = time.Unix(m.snapshot.CostsStart, 0).UTC().Format("Jan 2006") + " · UTC"
	}
	limit := limitsSafeLine(m.sourceSummary(0))
	costs := limitsSafeLine(m.costsSummary())
	enforcement := "Not reported"
	if m.snapshot.Limit.Exists() && m.limitReadable() && !m.snapshot.LimitAbsent && !m.snapshot.LimitUnconfirmed {
		state := m.snapshot.Limit.Get("enforcement.status")
		if limitsUniqueJSON(m.snapshot.Limit) != nil {
			enforcement = "Ambiguous · inspect original"
		} else if state.Type == gjson.String {
			enforcement = limitsSafeLine(state.String())
		}
	}
	property, value := "Policy", enforcement
	readout := []string{limit, costs}
	if m.section == 0 {
		if meter, ok := m.spendMeter(style); ok {
			reported := style.reported.Render(" Reported USD " + m.snapshot.CostsTotal + " ")
			capAmount := strings.TrimSuffix(strings.TrimPrefix(limit, "Hard limit: "), " / month")
			cap := style.cap.Render(" Cap " + capAmount + "/mo ")
			readout = []string{reported + " " + cap, meter}
		}
	} else {
		property, value = "Effect", "Notifications only"
		if m.section == 2 {
			property, value = "Group", "By model"
		}
		if m.section == 1 && m.selection < len(menu) && menu[m.selection].kind == "alert_update" {
			property, value = "ID", limitsSafeLine(menu[m.selection].value.Get("id").String())
		}
		spend := costs
		if m.snapshot.CostsComplete && m.snapshot.CostsTotal != "" {
			spend = style.muted.Render("Reported spend  ") + "USD " + m.snapshot.CostsTotal
		}
		readout = []string{lipgloss.NewStyle().Bold(m.color).Render(m.sourceSummary(m.section)), spend}
	}
	view := setting("Tabs", strings.Join(tabs, "  "))
	context := []string{setting("Scope", limitsSafeLine(m.scope())), setting("Costs", period), view, setting(property, value)}
	if !m.framed() {
		// The compact title already shows scope. Preserve room for recovery and the selected action.
		context = []string{view, setting(property, value)}
	}
	var notes []string
	if m.note != "" {
		notes = strings.Split(ansi.Wrap(limitsSafeLine(m.note), width, ""), "\n")
	}
	if m.locked {
		notes = append(notes, "Writes locked. Inspect before retry.")
	}
	left := (width - 3) * 45 / 100
	right := width - left - 3
	// Choose one arrangement for every tab, including unusually long costs or policy values.
	split := m.width >= 96 && m.framed() &&
		ansi.StringWidth(limit) <= right-2 && ansi.StringWidth(costs) <= right-2 && ansi.StringWidth(enforcement)+9 <= left
	if !split {
		right = width
	}
	if ansi.StringWidth(readout[0])+2 > right && m.section == 0 {
		readout = []string{limit, costs}
	}
	items := append([]string(nil), readout...)
	inset := "  "
	if !m.framed() {
		inset = ""
	}
	for i, row := range items {
		items[i] = inset + ansi.Truncate(row, right-len(inset), "…")
	}
	free := m.panelHeight() - m.panelChrome() - footerRows - len(notes) - len(items)
	if !split {
		free -= len(context)
	}
	count := max(1, min(free, max(2, len(m.snapshot.Alerts)+1, len(m.snapshot.Rates))))
	start := max(0, m.selection-count+1)
	for i := 0; i < count; i++ {
		row := ""
		switch pos := start + i; {
		case pos < len(menu):
			label := ansi.Truncate(limitsSafeLine(menu[pos].label), right-2, "…")
			if pos == m.selection {
				row = style.selected.Render("› " + label)
			} else {
				row = "  " + label
			}
		case len(menu) == 0 && i == 0:
			row = "  No editable records loaded."
		}
		items = append(items, row)
	}
	if !split {
		return append(append(context, items...), notes...)
	}
	body := make([]string, 0, max(len(context), len(items))+len(notes))
	for i := 0; i < max(len(context), len(items)); i++ {
		col, item := "", ""
		if i < len(context) {
			col = ansi.Truncate(context[i], left, "…")
		}
		if i < len(items) {
			item = items[i]
		}
		body = append(body, col+strings.Repeat(" ", left-ansi.StringWidth(col)+1)+style.border.Render("│")+" "+item)
	}
	return append(body, notes...)
}

// Escape only a display copy, before the input applies cursor and overflow layout.
func limitsInputView(input textinput.Model) string {
	value := input.Value()
	escaped := readable.Text(value)
	if escaped != value {
		position := input.Position()
		prefix := string([]rune(value)[:position])
		input.Reset()
		input.SetValue(escaped)
		input.SetCursor(utf8.RuneCountInString(readable.Text(prefix)))
	}
	return input.View()
}

// Keep external text on one row after escaping terminal controls.
func limitsSafeLine(value string) string {
	return strings.NewReplacer("\n", " ↵ ", "\t", " → ").Replace(readable.Text(value))
}
