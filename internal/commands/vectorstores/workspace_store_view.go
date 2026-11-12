package vectorstores

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
)

const storeFinderLabel = "Find store"

// Index zero is the store list. The other entries are store actions.
var storePickerActions = [...]struct{ label, enter, hint string }{
	{enter: "open store"},
	{"Create store", "choose name", "Choose a name, then confirm to create it."},
	{"Refresh stores", "refresh", "Reload the store list from this project."},
	{"Load more stores", "load more", "Load the next page of stores from this project."},
}

func (m *workspace) storeFooter() string {
	if m.page == "filter" {
		return "Enter/Tab apply filter · Esc cancel"
	}
	destinations := [...]string{"stores", "actions", "next action", "next action", "find store"}
	tab := "Tab " + destinations[m.nextStoreFocus(1)]
	if m.focus == storeFinderFocus {
		return "Type to filter · " + tab + " · Esc exit"
	}
	if m.bodyWidth() < 40 {
		return "Enter " + storePickerActions[m.focus].enter + " · Esc exit · ↕ move · " + tab
	}
	return "↑↓ move · " + tab + " · Enter " + storePickerActions[m.focus].enter + " · Esc exit"
}

func (m *workspace) storeFinderRow(width int) string {
	if m.page == "filter" {
		return m.accent().Bold(m.color).Render("› "+storeFinderLabel) + "  " + m.editorField()
	}
	query := cleanLine(m.storeFilter)
	if query == "" {
		query = "Name or ID"
	}
	if m.focus == storeFinderFocus {
		return m.rowWidth(storeFinderLabel+"  "+query, true, width)
	}
	return m.accent().Bold(m.color).Render("/") + " " + m.heading(storeFinderLabel) + "  " + m.muted().Render(query)
}

func (m *workspace) storeLines(lines []string, footer string) []string {
	choices := m.storeChoices()
	width := m.bodyWidth()
	editing := m.page == "filter"
	spacer := m.framed() && m.height >= 20 && !editing
	var picker, details []string
	var columns []workspaceColumn
	var items [][]string
	if m.wideLayout() {
		width, _ = m.paneWidths()
		stores := m.filteredStores()
		columns, items = storePickerTable(stores, width)
		picker = []string{m.storeFinderRow(width), m.heading("Stores")}
		if m.focus == 0 && m.selected >= 0 && m.selected < len(stores) {
			store := &stores[m.selected]
			details = m.storeInspector(store)
			if !editing && store.Status != "expired" {
				details = append(details, "", m.muted().Render("Open this store to upload or view files."))
			}
		} else if m.focus == storeFinderFocus {
			details = []string{m.muted().Render("Filter loaded stores by name or ID.")}
		} else if m.focus > 0 {
			action := storePickerActions[m.focus]
			_, detailWidth := m.paneWidths()
			heading, hint := action.label, action.hint
			if _, diagnostic := m.storeFeedback(); diagnostic != "" {
				heading, hint = "Error details", diagnostic
			}
			details = []string{m.heading(heading)}
			for _, line := range strings.Split(hint, "\n") {
				for _, row := range strings.Split(ansi.Wrap(cleanLine(line), detailWidth, ""), "\n") {
					details = append(details, m.muted().Render(row))
				}
			}
		}
	} else {
		columns, items = m.storeTable()
		picker = []string{m.storeFinderRow(width), m.tableHeader(columns)}
	}
	if len(items) == 0 && !m.storesLoaded && m.lastErr != nil && m.note != "" {
		// The error explains why the list is unavailable. Keep the finder and actions.
		picker = picker[:1]
	} else if len(items) == 0 {
		message := "No stores loaded."
		if !m.storesLoaded && m.lastErr != nil && len(m.stores) == 0 {
			message = "Could not load stores."
		} else if m.storeFinderQuery() != "" {
			message = "No matching stores."
		}
		picker = append(picker, m.muted().Render("  "+message))
	} else {
		trailing := len(picker)
		if !editing {
			trailing += 1 + len(choices) // Reserve the actions heading and rows.
		}
		if spacer {
			trailing++
		}
		count := m.listBudget(lines, footer, trailing)
		picker = append(picker, m.tableRows(columns, items, m.selected, count, m.focus == 0 && !editing)...)
	}
	if !editing {
		if spacer {
			picker = append(picker, "")
		}
		if m.framed() {
			picker = append(picker, m.heading("Actions"))
		} else {
			picker = append(picker, m.divider())
		}
		for _, focus := range choices {
			picker = append(picker, m.rowWidth(storePickerActions[focus].label, m.focus == focus, width))
		}
	}
	if m.wideLayout() {
		return m.appendPanes(lines, picker, details, footer)
	}
	return append(lines, picker...)
}

// When the first refresh fails, a wide terminal can show diagnostics beside the recovery instructions.
func (m *workspace) storeFeedback() (message, details string) {
	if m.wideLayout() && m.page == "stores" && !m.storesLoaded && m.focus == 2 && m.lastErr != nil {
		message, details, _ = strings.Cut(m.note, "\n\n")
		return message, details
	}
	return m.note, ""
}

// Only names that occur more than once need an ID in the picker.
func storePickerTable(stores []openai.VectorStore, width int) ([]workspaceColumn, [][]string) {
	names := make(map[string]int, len(stores))
	items := make([][]string, 0, len(stores))
	for _, store := range stores {
		name := cleanLine(storeDisplayName(store.Name))
		items = append(items, []string{name, cleanLine(store.ID)})
		names[name]++
	}
	idWidth := 0
	for _, item := range items {
		if names[item[0]] > 1 {
			idWidth = max(idWidth, ansi.StringWidth(item[1]))
		} else {
			item[1] = ""
		}
	}
	columns := []workspaceColumn{{}}
	if idWidth > 0 {
		idWidth = min(idWidth, width-2-len(workspaceColumnGap)-10)
		columns = append(columns, workspaceColumn{width: idWidth})
		for _, item := range items {
			if cells := ansi.StringWidth(item[1]); cells > idWidth {
				item[1] = ansi.TruncateLeft(item[1], cells-idWidth+1, "…")
			}
		}
	}
	sizeTable(columns, width)
	return columns, items
}

func (m *workspace) storeInspector(store *openai.VectorStore) []string {
	status := string(store.Status)
	if status == "" {
		status = "unknown"
	}
	files := "Unknown"
	if store.FileCounts.JSON.Total.Valid() || store.FileCounts.Total != 0 {
		files = fmt.Sprint(store.FileCounts.Total)
	}
	label := m.muted()
	lines := []string{
		m.heading("Store details"),
		label.Render("Status  ") + m.status(status),
		label.Render("Files   ") + files,
	}
	_, width := m.paneWidths()
	const firstLabel = "ID      "
	for i, part := range strings.Split(ansi.Hardwrap(cleanLine(store.ID), max(1, width-len(firstLabel)), true), "\n") {
		prefix := firstLabel
		if i > 0 {
			prefix = strings.Repeat(" ", len(firstLabel))
		}
		lines = append(lines, label.Render(prefix+part))
	}
	return lines
}
