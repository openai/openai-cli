package audio

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *terminalTranslationModel) sizeTranslationEditor(edit *textarea.Model) {
	width, height := m.translationViewport()
	edit.SetWidth(max(1, width-1)) // Keep a cell for the caret at the end of a line.
	edit.SetHeight(max(1, min(6, height-7)))
}

func (m *terminalTranslationModel) setupView(l translationLayout) tea.View {
	field := m.fields()[m.form.row]
	title := "Live translation"
	var controls [][2]string
	var rows []string
	var focus int
	var cursor *tea.Cursor
	switch {
	case m.form.editor != nil:
		title = "Edit " + strings.ToLower(field)
		if field == "File" {
			title = "Edit audio file"
		}
		edit := m.form.editor
		rows = strings.Split(edit.View(), "\n")
		cursor = audioEditorCursor(*edit, 0, 3)
		controls = [][2]string{{"Enter", "apply"}, {"Esc", "cancel"}}
		if field == "Text" {
			controls = append(controls, [2]string{"Alt+Enter", "new line"})
		}
	case m.form.languagesOpen:
		title = "Translate into"
		focus = m.form.languageIndex
		for i, code := range translationLanguages {
			label := audiolive.LanguageName(code)
			if code == m.options.Config.Language {
				label += "  current"
			}
			rows = append(rows, m.translationChoice(l.width, label, i == focus))
		}
		controls = [][2]string{{"↑↓", "move"}, {"Enter", "select"}, {"Esc", "back"}}
	default:
		rows, focus = m.setupRows(l)
		controls = m.setupKeys(field)
	}
	shortcuts := l.footer(controls...)
	header := []string{l.title.Render(title), l.divider, ""}
	var notice []string
	if m.note != "" && !m.form.languagesOpen {
		style := audioFailureStyle(m.color, m.dark)
		wrapped := strings.Split(ansi.Wrap(readable.Text(m.note), l.width, ""), "\n")
		limit := 3
		if m.form.editor != nil {
			limit = 2
		}
		if len(wrapped) > limit {
			wrapped[limit-1] = ansi.Truncate(strings.Join(wrapped[limit-1:], " "), l.width, "…")
			wrapped = wrapped[:limit]
		}
		notice = append(notice, "")
		for _, line := range wrapped {
			notice = append(notice, style.Render(line))
		}
	}
	available := max(1, l.height-len(header)-len(shortcuts)-len(notice))
	start := max(0, focus-available+1)
	rows = append(header, rows[start:min(len(rows), start+available)]...)
	rows = append(rows, notice...)
	return l.panel(rows, shortcuts, cursor)
}

func (m *terminalTranslationModel) setupRows(l translationLayout) ([]string, int) {
	const labelWidth = 16
	var rows []string
	var focus int
	selected := audioSelectionStyle(m.color, m.dark)
	for i, field := range m.fields() {
		active := m.form.row == i
		var line string
		switch field {
		case "Source":
			options := make([]string, 0, len(translationSources))
			for j, option := range translationSources {
				if translationSource(j) != m.form.source {
					options = append(options, l.muted.Render(option))
					continue
				}
				option = "[" + option + "]"
				if active {
					option = selected.Render(option)
				} else {
					option = l.title.Render(option)
				}
				options = append(options, option)
			}
			line = strings.Join(options, "  ")
			label := l.muted.Render(fmt.Sprintf("%-*s", labelWidth, "Translate from"))
			if ansi.StringWidth(line)+labelWidth+2 <= l.width {
				line = label + line
			} else {
				rows = append(rows, "  "+l.muted.Render("Translate from"))
				if ansi.StringWidth(line)+2 > l.width {
					line = strings.Join(options, " ")
				}
			}
			marker := "  "
			if active {
				focus = len(rows)
				marker = l.accent.Render("› ")
			}
			rows = append(rows, marker+line)
			continue
		case "Start":
			rows = append(rows, "")
			line = l.title.Render(m.setupAction())
		case "More settings":
			chevron := "▸"
			if m.form.advanced {
				chevron = "▾"
			}
			line = l.muted.Render(field + "  " + chevron)
		default:
			label := field
			switch field {
			case "Language":
				label = "Translate into"
			case "File":
				label = "Audio file"
			}
			line = l.muted.Render(fmt.Sprintf("%-*s", labelWidth, label)) + m.fieldValue(field)
		}
		if active {
			focus = len(rows)
		}
		rows = append(rows, m.translationChoice(l.width, line, active))
	}
	return rows, focus
}

func (m *terminalTranslationModel) translationChoice(width int, line string, active bool) string {
	if !active {
		return "  " + line
	}
	line = ansi.Truncate("› "+ansi.Strip(line), width, "…")
	return audioSelectionStyle(m.color, m.dark).Render(line + strings.Repeat(" ", width-ansi.StringWidth(line)))
}

func (m *terminalTranslationModel) setupKeys(field string) [][2]string {
	pairs := [][2]string{{"↑↓", "move"}}
	action := "edit"
	switch field {
	case "Source":
		pairs = append(pairs, [2]string{"←→", "input"})
		action = "choose"
	case "Start":
		pairs = append(pairs, [2]string{"←→", "input"})
		action = "start"
		if label := m.setupAction(); label == "Enter text" || label == "Enter file path" {
			action = "edit"
		}
	case "Language":
		action = "languages"
	case "More settings":
		action = "expand"
		if m.form.advanced {
			action = "collapse"
		}
	case "Playback", "Noise reduction":
		action = "toggle"
	}
	return append(pairs, [2]string{"Enter", action}, [2]string{"Esc", "exit"})
}
