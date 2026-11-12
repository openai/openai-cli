package audio

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/readable"
)

type terminalTextTranslator func(context.Context, string, string, string) (translationTextResult, error)

type translationSource int

const (
	translationMicrophone translationSource = iota
	translationFile
	translationText
)

var translationSources = [...]string{"Microphone", "Audio file", "Typed text"}
var translationLanguages = []string{"zh", "en", "fr", "de", "hi", "id", "it", "ja", "ko", "pt", "ru", "es", "vi"}

type terminalTranslationForm struct {
	source                  translationSource
	row                     int
	advanced, languagesOpen bool
	languageIndex           int
	text, path, textModel   string
	editor                  *textarea.Model
}

func (m *terminalTranslationModel) fields() []string {
	rows := []string{"Source", "Language"}
	switch m.form.source {
	case translationFile:
		rows = append(rows, "File")
	case translationText:
		rows = append(rows, "Text")
	}
	rows = append(rows, "Start", "More settings")
	if !m.form.advanced {
		return rows
	}
	switch m.form.source {
	case translationText:
		return append(rows, "Text model", "Save folder")
	case translationMicrophone:
		rows = append(rows, "Microphone")
	}
	return append(rows, "Audio model", "Transcription", "Playback", "Noise reduction", "Save folder")
}

func (m *terminalTranslationModel) focusSetupAction() {
	m.form.row = slices.Index(m.fields(), "Start")
}

func (m *terminalTranslationModel) setupAction() string {
	if m.retrySetup {
		return "Retry translation"
	}
	switch m.form.source {
	case translationText:
		if strings.TrimSpace(m.form.text) == "" {
			return "Enter text"
		}
		return "Translate text"
	case translationFile:
		if strings.TrimSpace(m.form.path) == "" {
			return "Enter file path"
		}
		return "Translate file"
	default:
		return "Start microphone"
	}
}

func (m *terminalTranslationModel) editableField(field string) *string {
	switch field {
	case "File":
		return &m.form.path
	case "Text":
		return &m.form.text
	case "Text model":
		return &m.form.textModel
	case "Audio model":
		return &m.options.Config.Model
	case "Transcription":
		return &m.options.Config.TranscriptionModel
	case "Microphone":
		return &m.options.Device
	case "Save folder":
		return &m.folder
	}
	return nil
}

func (m *terminalTranslationModel) fieldValue(field string) string {
	onOff := func(enabled bool) string {
		if enabled {
			return "On"
		}
		return "Off"
	}
	switch field {
	case "Source":
		return translationSources[m.form.source]
	case "Language":
		return audiolive.LanguageName(m.options.Config.Language)
	case "Playback":
		return onOff(m.options.Listen)
	case "Noise reduction":
		return onOff(m.options.Config.NoiseReduction)
	}
	value := *m.editableField(field)
	if value != "" {
		return strings.Join(strings.Fields(readable.Text(value)), " ")
	}
	switch field {
	case "Microphone":
		return "System default"
	case "File":
		return "Enter a local file path"
	case "Text":
		return "Enter text to translate"
	}
	return "Default"
}

func (m *terminalTranslationModel) openField(field string) tea.Cmd {
	edit := textarea.New()
	edit.CharLimit = 0
	edit.MaxHeight = 0
	edit.KeyMap.Paste.SetEnabled(false)
	edit.KeyMap.CopySelection.SetEnabled(false)
	edit.SetVirtualCursor(false)
	edit.ShowLineNumbers, edit.Prompt = false, ""
	_, muted, accent := m.styles()
	var styles textarea.Styles
	styles.Focused.Placeholder = muted
	styles.Focused.Selection = accent
	edit.SetStyles(styles)
	edit.SetWidth(max(1, min(100, m.width-4)))
	edit.SetHeight(max(1, min(6, m.height-7)))
	edit.SetValue(readable.Text(*m.editableField(field)))
	switch field {
	case "File":
		edit.Placeholder = "Path to your audio file"
	case "Text":
		edit.Placeholder = "Type or paste text to translate"
	}
	m.form.row = slices.Index(m.fields(), field)
	m.form.editor = &edit
	return edit.Focus()
}

func (m *terminalTranslationModel) updateField(message tea.Msg) tea.Cmd {
	edit := m.form.editor
	field := m.fields()[m.form.row]
	switch msg := message.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			m.form.editor = nil
			return nil
		case "enter", "ctrl+g":
			value := edit.Value()
			if field != "Text" && strings.ContainsAny(value, "\n\r\t") {
				m.note = "Enter a single line for this setting."
				return nil
			}
			*m.editableField(field) = value
			m.form.editor, m.note, m.retrySetup = nil, "", false
			if field == "File" || field == "Text" {
				m.focusSetupAction()
			}
			if msg.String() == "ctrl+g" {
				return m.startTranslation()
			}
			return nil
		case "alt+enter":
			if field == "Text" {
				edit.InsertString("\n")
			}
			return nil
		}
		msg.Text = readable.Text(msg.Text)
		message = msg
	case tea.PasteMsg:
		edit.InsertString(readable.Text(audioEditorText(msg.Content)))
		return nil
	}
	next, cmd := edit.Update(message)
	*edit = next
	return cmd
}

func (m *terminalTranslationModel) setupKey(key string) tea.Cmd {
	fields := m.fields()
	field := fields[m.form.row]
	step := 1
	if key == "left" || key == "up" || key == "shift+tab" {
		step = -1
	}
	if m.form.languagesOpen {
		switch key {
		case "esc":
			m.form.languagesOpen = false
		case "up", "down", "tab", "shift+tab":
			m.form.languageIndex = (m.form.languageIndex + len(translationLanguages) + step) % len(translationLanguages)
		case "enter":
			m.options.Config.Language = translationLanguages[m.form.languageIndex]
			m.form.languagesOpen = false
			m.focusSetupAction()
		}
		return nil
	}
	switch key {
	case "tab", "down", "shift+tab", "up":
		m.form.row = (m.form.row + len(fields) + step) % len(fields)
	case "left", "right", "space":
		switch field {
		case "Source", "Start":
			m.form.source = translationSource((int(m.form.source) + len(translationSources) + step) % len(translationSources))
			m.form.row = 0
			m.note, m.retrySetup = "", false
		case "Language":
			i := slices.Index(translationLanguages, m.options.Config.Language)
			m.options.Config.Language = translationLanguages[(i+len(translationLanguages)+step)%len(translationLanguages)]
		case "Playback":
			m.options.Listen = !m.options.Listen
		case "Noise reduction":
			m.options.Config.NoiseReduction = !m.options.Config.NoiseReduction
		case "More settings":
			m.form.advanced = key == "right" || (key == "space" && !m.form.advanced)
		}
	case "enter":
		switch field {
		case "Language":
			m.form.languagesOpen = true
			m.form.languageIndex = max(0, slices.Index(translationLanguages, m.options.Config.Language))
		case "More settings":
			m.form.advanced = !m.form.advanced
		case "Playback":
			m.options.Listen = !m.options.Listen
		case "Noise reduction":
			m.options.Config.NoiseReduction = !m.options.Config.NoiseReduction
		case "Source":
			if m.form.source == translationFile && strings.TrimSpace(m.form.path) == "" {
				return m.openField("File")
			}
			if m.form.source == translationText && strings.TrimSpace(m.form.text) == "" {
				return m.openField("Text")
			}
			m.focusSetupAction()
		case "Start":
			return m.startTranslation()
		default:
			return m.openField(field)
		}
	case "ctrl+g":
		return m.startTranslation()
	}
	return nil
}

func (m *terminalTranslationModel) setupView(width int) tea.View {
	title, muted, accent := m.styles()
	selected := audioSelectionStyle(m.color, m.dark)
	fields := m.fields()
	field := fields[m.form.row]
	rows := []string{title.Render("Live translation")}
	// Fixed header, footer and action remain legible even at the minimum size.
	var body, footer []string
	focusRow := 0
	var cursor *tea.Cursor
	keyHint := func(key, action string) string { return accent.Render(key) + " " + muted.Render(action) }
	switch {
	case m.form.editor != nil:
		edit := m.form.editor
		rows = append(rows, "", title.Render(field))
		cursor = audioEditorCursor(*edit, 2, len(rows))
		body = strings.Split(edit.View(), "\n")
		footer = []string{keyHint("Enter", "apply") + "   " + keyHint("Esc", "cancel")}
		if field == "Text" {
			footer = append(footer, keyHint("Alt+Enter", "new line"))
		}
	case m.form.languagesOpen:
		rows = []string{title.Render("Translate into")}
		for i, code := range translationLanguages {
			label := "  " + audiolive.LanguageName(code)
			if code == m.options.Config.Language {
				label += "  current"
			}
			if i == m.form.languageIndex {
				label = selected.Render("› " + label[2:])
				focusRow = i
			}
			body = append(body, label)
		}
		footer = []string{keyHint("↑↓", "move") + "  " + keyHint("Enter", "select") + "  " + keyHint("Esc", "back")}
	default:
		if !m.form.advanced && m.note == "" {
			guides := [...]string{"See a translation as you speak.", "Translate speech from a local file.", "Translate text you type or paste."}
			rows = append(rows, muted.Render(guides[m.form.source]))
		}
		rows = append(rows, "")
		body = append(body, muted.Render("Translate from"))
		for i, name := range fields {
			active := i == m.form.row
			marker := "  "
			if active {
				marker = accent.Render("› ")
			}
			var line string
			switch name {
			case "Source":
				var options []string
				for j, option := range translationSources {
					if translationSource(j) == m.form.source {
						option = "[" + option + "]"
						if active {
							option = selected.Render(option)
						} else {
							option = title.Render(option)
						}
					} else {
						option = muted.Render(option)
					}
					options = append(options, option)
				}
				gap := "   "
				if width < 44 {
					gap = " "
				}
				line = strings.Join(options, gap)
			case "Start":
				body = append(body, "")
				line = title.Render(m.setupAction())
				shortcut := "Ctrl+G"
				if active {
					shortcut = "Enter"
				}
				line += "   " + accent.Render(shortcut)
			case "More settings":
				chevron := "▸"
				if m.form.advanced {
					chevron = "▾"
				}
				line = muted.Render("More settings  " + chevron)
			default:
				label := name
				switch name {
				case "Language":
					label = "Translate into"
				case "File":
					label = "Audio file"
				}
				line = muted.Render(fmt.Sprintf("%-16s", label)) + m.fieldValue(name)
			}
			if active {
				focusRow = len(body)
				if name != "Source" {
					line = selected.Render("› " + ansi.Strip(line))
					marker = ""
				}
			}
			body = append(body, marker+line)
		}
		context := keyHint("Enter", "edit")
		switch field {
		case "Source":
			context = keyHint("←→", "input") + "  " + keyHint("Enter", "choose")
		case "Start":
			context = keyHint("←→", "input")
		case "Language":
			context = keyHint("Enter", "languages")
		case "More settings":
			verb := "expand"
			if m.form.advanced {
				verb = "collapse"
			}
			context = keyHint("Enter", verb)
		case "Playback", "Noise reduction":
			context = keyHint("Enter", "toggle")
		}
		move, exit := context+"  "+keyHint("↑↓", "move"), keyHint("Esc", "exit")
		if ansi.StringWidth(move+"  "+exit) <= width {
			footer = []string{move + "  " + exit}
		} else {
			footer = []string{move, exit}
		}
	}
	if m.note != "" {
		failure := audioFailureStyle(m.color, m.dark)
		messages := strings.Split(ansi.Wrap(readable.Text(m.note), width, ""), "\n")
		for i := range messages {
			messages[i] = failure.Render(messages[i])
		}
		footer = append(messages, footer...)
	}
	remaining := max(1, m.height-len(rows)-len(footer)-1)
	start := max(0, focusRow-remaining+1)
	rows = append(rows, body[start:min(len(body), start+remaining)]...)
	rows = append(rows, "")
	rows = append(rows, footer...)
	for i := range rows {
		rows[i] = "  " + ansi.Truncate(rows[i], width, "…")
	}
	view := tea.NewView(strings.Join(rows, "\n"))
	view.Cursor = cursor
	return view
}

// A typed translation uses the same private caption files and explicit Save action as an audio session.
func runTerminalText(ctx context.Context, translate terminalTextTranslator, text, language, model string) (terminalTranslationResult, terminalTranslationUpdate) {
	start := time.Now()
	response, err := translate(ctx, text, audiolive.LanguageName(language), model)
	if ctx.Err() != nil {
		return terminalTranslationResult{Err: ctx.Err()}, terminalTranslationUpdate{State: "Stopped"}
	}
	state := terminalTranslationUpdate{State: "finished", Original: terminalTranslationTail("", text), Latency: time.Since(start), HasLatency: err == nil, InputTokens: response.InputTokens, OutputTokens: response.OutputTokens}
	result := terminalTranslationResult{Err: err}
	if err != nil {
		return result, state
	}
	state.Translation = terminalTranslationTail("", response.Text)
	stage, err := newTerminalTranslationStage()
	if err == nil {
		result.stage, result.Directory = stage, stage.directory
		result.OriginalPath = filepath.Join(stage.directory, stage.names[1])
		result.TranslationPath = filepath.Join(stage.directory, stage.names[2])
		_, err = stage.files[1].WriteString(text)
		if err == nil {
			_, err = stage.files[2].WriteString(response.Text)
		}
		if closeErr := stage.finish(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		result.Err = audioFailure("Could not retain the translated text. Check temporary-folder access.", err)
	}
	result.Complete = result.Err == nil
	return result, state
}

func (m *terminalTranslationModel) metricSummary() string {
	var parts []string
	if m.state.HasLatency {
		label := "Caption latency"
		if m.form.source == translationText {
			label = "Response time"
		}
		parts = append(parts, fmt.Sprintf("%s %d ms", label, m.state.Latency.Milliseconds()))
	}
	if m.state.InputTokens != nil {
		parts = append(parts, fmt.Sprintf("Input %d tokens", *m.state.InputTokens))
	}
	if m.state.OutputTokens != nil {
		parts = append(parts, fmt.Sprintf("Output %d tokens", *m.state.OutputTokens))
	}
	if m.state.InputTokens == nil && m.state.OutputTokens == nil && m.state.Tokens > 0 {
		parts = append(parts, fmt.Sprintf("Total %d tokens", m.state.Tokens))
	}
	return strings.Join(parts, "  ")
}
