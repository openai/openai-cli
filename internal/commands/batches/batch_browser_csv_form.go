package batches

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func newBatchBrowserCSVForm(csv *batchBrowserCSV) *batchBrowserScreen {
	m := newBatchBrowserFlow("csv", "Set up CSV", filepath.Base(csv.path)+"  /  One request per row", nil)
	m.flow.csv = csv
	m.input.Focus()
	m.input.SetVirtualCursor(false)
	if len(csv.header) == 1 {
		csv.column = csv.header[0]
	}
	m.csvFields(csv.column == "")
	// Start on the first column choice, or directly on instructions for a known column.
	m.focusCSVField(1)
	return m
}

func (m *batchBrowserScreen) csvFields(open bool) {
	csv := m.flow.csv
	column := csv.column
	if column == "" {
		column = "Choose a column"
	}
	m.choices = []batchBrowserChoice{{value: "column", title: "Input column", setting: column}}
	if open {
		for i, name := range csv.header {
			m.choices = append(m.choices, batchBrowserChoice{value: "col:" + strconv.Itoa(i), title: "  " + name})
		}
	}
	m.choices = append(m.choices,
		batchBrowserChoice{value: "task", title: "Instructions", setting: csv.instructions, detail: "What should the model do with each row?"},
		batchBrowserChoice{value: "model", title: "Model", setting: csv.model, detail: "Use a Responses model available to your project."},
		batchBrowserChoice{value: "review", title: "Review batch"})
	m.filter()
}

func (m *batchBrowserScreen) focusCSVField(index int) {
	m.index = max(0, min(index, len(m.choices)-1))
	row := m.choices[m.index]
	m.input.Reset()
	m.notice = ""
	switch row.value {
	case "task":
		m.input.Placeholder = "Describe the task"
		m.input.SetValue(m.flow.csv.instructions)
	case "model":
		m.input.Placeholder = "Enter a model name"
		m.input.SetValue(m.flow.csv.model)
	}
}

func (m *batchBrowserScreen) reviewCSVForm() (tea.Model, tea.Cmd) {
	csv := m.flow.csv
	if csv.column == "" {
		m.csvFields(true)
		m.focusCSVField(1)
		m.notice = "Choose the column that contains your input text."
	} else if strings.TrimSpace(csv.instructions) == "" || strings.TrimSpace(csv.model) == "" {
		field, notice := "task", "Enter instructions for each row."
		if strings.TrimSpace(csv.instructions) != "" {
			field, notice = "model", "Enter a model name."
		}
		m.focusCSVField(slices.IndexFunc(m.choices, func(c batchBrowserChoice) bool { return c.value == field }))
		m.notice = notice
	} else {
		m.closed, m.chosen = true, "review"
		return m, tea.Quit
	}
	return m, nil
}

func (m *batchBrowserScreen) updateCSVForm(message tea.Msg) (tea.Model, tea.Cmd) {
	row := &m.choices[m.index]
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc", "ctrl+c":
			m.closed, m.chosen = true, "back"
			return m, tea.Quit
		case "up", "shift+tab":
			m.focusCSVField(m.index - 1)
			return m, nil
		case "down", "tab":
			m.focusCSVField(m.index + 1)
			return m, nil
		case "enter":
			if m.width < 30 || m.height < 7 || key.IsRepeat {
				return m, nil
			}
			if id, ok := strings.CutPrefix(row.value, "col:"); ok {
				i, _ := strconv.Atoi(id) // Generated from this header by csvFields.
				m.flow.csv.column = m.flow.csv.header[i]
				m.csvFields(false)
				m.focusCSVField(1)
				return m, nil
			}
			switch row.value {
			case "column":
				m.csvFields(true)
				m.focusCSVField(1 + max(0, slices.Index(m.flow.csv.header, m.flow.csv.column)))
			case "task":
				if strings.TrimSpace(m.input.Value()) == "" {
					m.notice = "Enter instructions for each row."
				} else {
					m.focusCSVField(m.index + 1)
				}
			default:
				return m.reviewCSVForm()
			}
			return m, nil
		}
	}
	if row.value != "task" && row.value != "model" {
		return m, nil
	}
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	row.setting = m.input.Value()
	if row.value == "task" {
		m.flow.csv.instructions = row.setting
	} else {
		m.flow.csv.model = row.setting
	}
	m.notice = ""
	return m, command
}
