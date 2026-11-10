package vectorstores

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
)

type detailField struct{ label, value string }

func storeDetails(store *openai.VectorStore, now time.Time) []detailField {
	expiration := "Never"
	if store.ExpiresAfter.Days > 0 {
		expiration = fmt.Sprintf("%d days since last activity", store.ExpiresAfter.Days)
	}
	estimate := "Not available"
	if store.CreatedAt > 0 {
		estimate = fmt.Sprintf("%.4f GiB-hours (current-size estimate)", estimatedStorageHours(store, now)/(1<<30))
	}
	return []detailField{
		{"ID", store.ID}, {"Name", store.Name}, {"Status", string(store.Status)},
		{"Size", fmt.Sprintf("%d bytes", store.UsageBytes)}, {"Estimated usage this month", estimate},
		{"Created", displayTimestamp(store.CreatedAt)}, {"Last active", displayTimestamp(store.LastActiveAt)},
		{"Expires", displayTimestamp(store.ExpiresAt)}, {"Expiration", expiration},
		{"Files", compactJSON(store.FileCounts)}, {"Metadata", compactJSON(store.Metadata)},
	}
}

func (m *workspace) openDetails(title string, fields []detailField, back string) tea.Cmd {
	var text strings.Builder
	for _, field := range fields {
		fmt.Fprintf(&text, "%s: %s\n", field.label, field.value)
	}
	cmd := m.openDocument(title, text.String(), back)
	m.documentFields = fields
	m.layoutPassage()
	return cmd
}

func (m *workspace) detailLines(fields []detailField) []string {
	labelWidth := 0
	for _, field := range fields {
		labelWidth = max(labelWidth, ansi.StringWidth(field.label))
	}
	stacked := m.bodyWidth()-labelWidth-3 < 24
	valueWidth := m.bodyWidth() - labelWidth - 3
	if stacked {
		valueWidth = m.bodyWidth() - 2
	}
	var lines []string
	for _, field := range fields {
		label := m.muted().Render(field.label)
		indent := strings.Repeat(" ", labelWidth+3)
		if stacked {
			lines = append(lines, label)
			indent = "  "
		}
		for i, value := range strings.Split(ansi.Hardwrap(cleanLine(field.value), max(1, valueWidth), true), "\n") {
			prefix := indent
			if !stacked && i == 0 {
				prefix = label + strings.Repeat(" ", labelWidth-ansi.StringWidth(field.label)+3)
			}
			if field.label == "Status" {
				value = m.statusStyle(field.value).Render(value)
			}
			lines = append(lines, prefix+value)
		}
	}
	return lines
}

// Match the dashboard's current-size estimate over this UTC month's active window.
// This is a storage estimate, not a billing calculation.
func estimatedStorageHours(store *openai.VectorStore, now time.Time) float64 {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if created := time.Unix(store.CreatedAt, 0); created.After(start) {
		start = created
	}
	end := now
	if store.ExpiresAt > 0 && time.Unix(store.ExpiresAt, 0).Before(end) {
		end = time.Unix(store.ExpiresAt, 0)
	}
	return float64(max(0, store.UsageBytes)) * max(0, end.Sub(start).Hours())
}

func (m *workspace) managementLines() ([]string, string) {
	if menu := m.managementMenu(); menu != nil {
		footer := "↑↓ choose · Enter open · Esc back · Ctrl+C exit"
		lines := []string{m.divider()}
		if m.page == "manage" && m.store != nil {
			lines = append(lines, m.muted().Render(cleanLine(m.store.ID))+"  "+m.status(string(m.store.Status)))
		}
		if m.page == "file" && m.file != nil {
			name := m.names[m.file.ID]
			if name == "" {
				name = m.file.ID
			}
			lines = append(lines, m.muted().Render(cleanLine(name))+"  "+m.status(string(m.file.Status)))
		}
		if m.page == "batch" && m.batch != nil {
			lines = append(lines, m.muted().Render(cleanLine(m.batch.ID))+"  "+m.status(string(m.batch.Status)))
		}
		lines = append(lines, m.listRows(menu, m.selected, m.listBudget(lines, footer, 1), true)...)
		return lines, footer
	}
	if label, form := managementForms[m.page]; form {
		lines := m.editorLines(label)
		action := "Save"
		switch m.page {
		case "filter":
			action = "Search loaded stores"
		case "open-batch":
			action = "Open batch"
		case "delete-store":
			action = "Delete store"
		case "remove-file":
			action = "Remove attachment"
		case "cancel-batch":
			action = "Cancel processing"
		}
		lines = append(lines, m.divider(), m.actions([]string{action, "Back"}, 1))
		targetID := ""
		switch m.page {
		case "delete-store":
			targetID = m.store.ID
			warning := m.statusStyle("expired")
			lines = append(lines, warning.Render("Reference lookup is unavailable."), warning.Render("Deletion safety is not established"))
		case "remove-file":
			targetID = m.file.ID
			lines = append(lines, m.muted().Render("The source file stays in storage."))
		case "cancel-batch":
			targetID = m.batch.ID
			lines = append(lines, m.muted().Render("Completed files remain attached."))
		case "attach-options":
			lines = append(lines, m.muted().Render("{} uses automatic chunking. Options apply to the next attachment."), m.muted().Render(`Example: {"attributes":{"category":"notes"}}`))
		case "open-batch":
			lines = append(lines, m.muted().Render("The latest batch is prefilled. Older batches require a known ID."))
		}
		if targetID != "" {
			lines = append(lines, strings.Split(ansi.Hardwrap(cleanLine(targetID), m.bodyWidth(), true), "\n")...)
			if m.page == "delete-store" {
				lines = append(lines, m.muted().Render("Source files stay in storage."))
			}
		}
		footer := "↓ actions · Enter continue · Esc back"
		if m.focus > 0 {
			footer = "←→ action · Enter select · ↑ edit · Esc back"
		}
		if targetID != "" && m.bodyWidth() < 40 {
			footer = "Tab focus · Enter select · Esc back"
		}
		return lines, footer
	}
	if m.page == "sources" {
		footer := "↑↓ choose · Enter toggle file · Tab actions · Esc back"
		if m.focus > 0 {
			footer = "←→ action · Enter select · ↓ files · Esc back"
		}
		lines := []string{m.actions([]string{"Attach selected", "Load more", "Options", "Back"}, 1), m.divider()}
		columns, items := m.sourceTable()
		lines = append(lines, m.tableHeader(columns))
		lines = append(lines, m.tableRows(columns, items, m.selected, m.listBudget(lines, footer, 1), m.focus == 0)...)
		return lines, footer
	}
	return nil, ""
}
