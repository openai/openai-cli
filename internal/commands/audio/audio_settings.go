package audio

import (
	"math"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const audioSpeedError = "Enter a speed from 0.25 to 4."

type audioFormValidation struct {
	field   int
	preview bool
	message string
}

func audioAdvancedSetting(field int) bool {
	return field == 0 || field == 2 || field == 3
}

func (m *audioWorkbench) fieldValidation(field int) string {
	if field == 3 && m.speedError != "" {
		return m.speedError
	}
	if m.validation == nil || m.validation.field != field {
		return ""
	}
	switch field {
	case 2:
		return "Choose a supported audio format."
	case 3:
		return audioSpeedError
	default:
		return m.validation.message
	}
}

func (m *audioWorkbench) clearResolvedValidation() {
	if m.validation == nil {
		return
	}
	settings := m.effectiveSettings()
	if m.validation.preview {
		settings.Input = audioAuditionText
	}
	field, err := validateAudioFields(settings)
	if err != nil && field == m.validation.field {
		return
	}
	if m.note == m.validation.message {
		m.note = ""
	}
	m.validation = nil
}

func (m *audioWorkbench) beginInlineSpeed() tea.Cmd {
	m.speedEditing, m.speedError = true, ""
	m.speedInput.SetValue(strconv.FormatFloat(m.settings.Speed, 'f', -1, 64))
	m.speedInput.SelectAll()
	return m.speedInput.Focus()
}

func (m *audioWorkbench) cancelInlineSpeed() bool {
	if !m.speedEditing {
		return false
	}
	m.speedEditing, m.speedError = false, ""
	m.speedInput.Blur()
	return true
}

func audioSpeedValue(value string) (float64, bool) {
	speed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return speed, err == nil && !math.IsNaN(speed) && !math.IsInf(speed, 0) && speed >= .25 && speed <= 4
}

func (m *audioWorkbench) commitInlineSpeed() bool {
	speed, valid := audioSpeedValue(m.speedInput.Value())
	if !valid {
		m.speedError = audioSpeedError
		return false
	}
	m.settings.Speed = speed
	m.cancelInlineSpeed()
	return true
}

// Call before form navigation and generation shortcuts. A draft never changes
// the request until it validates; navigation cannot discard an invalid draft.
func (m *audioWorkbench) updateInlineSetting(message tea.Msg) (bool, tea.Cmd) {
	if !m.usable() || m.busy || m.modal != "" || m.focus != 2 || m.setting > 3 || m.setting < 0 {
		return false, nil
	}
	key, isKey := message.(tea.KeyPressMsg)
	if m.setting != 3 {
		if !isKey {
			return false, nil
		}
		return m.changeInlineChoice(key), nil
	}
	if m.speedEditing {
		if isKey {
			switch key.String() {
			case "esc":
				m.cancelInlineSpeed()
				return true, nil
			case "enter":
				m.commitInlineSpeed()
				return true, nil
			case "up", "down", "tab", "shift+tab", "home", "end", "ctrl+g", "ctrl+o", "ctrl+s", "ctrl+p", "f1":
				return !m.commitInlineSpeed(), nil
			case "left", "right":
				speed, valid := audioSpeedValue(m.speedInput.Value())
				if !valid {
					m.speedError = audioSpeedError
					return true, nil
				}
				m.speedInput.SetValue(strconv.FormatFloat(audioStepSpeed(speed, key.String()), 'f', -1, 64))
				m.speedInput.CursorEnd()
				m.speedError = ""
				return true, nil
			}
		}
		before := m.speedInput.Value()
		var cmd tea.Cmd
		m.speedInput, cmd = m.speedInput.Update(message)
		if m.speedInput.Value() != before {
			m.speedError = ""
		}
		return true, cmd
	}
	if isKey {
		switch key.String() {
		case "left", "right":
			m.settings.Speed = audioStepSpeed(m.settings.Speed, key.String())
			return true, nil
		case "enter":
			return true, m.beginInlineSpeed()
		}
	}
	_, pasted := message.(tea.PasteMsg)
	if !pasted && (!isKey || !strings.ContainsAny(audioSettingText(key), "0123456789.+-")) {
		return false, nil
	}
	m.beginInlineSpeed()
	m.speedInput.SetValue("")
	var cmd tea.Cmd
	m.speedInput, cmd = m.speedInput.Update(message)
	return true, cmd
}

func audioStepSpeed(speed float64, key string) float64 {
	step := .25
	if key == "left" {
		step = -.25
	}
	return max(.25, min(4, speed+step))
}

// Single-letter selection cycles matching choices without a hidden search draft.
// Voice P remains the explicit audition shortcut; Enter keeps the full chooser.
func (m *audioWorkbench) changeInlineChoice(key tea.KeyPressMsg) bool {
	choices := audioModels
	current := m.settings.Model
	switch m.setting {
	case 1:
		choices, current = audioModelVoices(m.settings.Model), m.settings.Voice
	case 2:
		choices, current = audioFormats, m.settings.Format
	}
	index := slices.Index(choices, current)
	next := index
	switch key.String() {
	case "left":
		if index < 0 {
			index = len(choices)
		}
		next = (index + len(choices) - 1) % len(choices)
	case "right":
		next = (index + 1) % len(choices)
	default:
		text := audioSettingText(key)
		if len(text) != 1 || m.setting == 1 && strings.EqualFold(text, "p") {
			return false
		}
		prefix := strings.ToLower(text)
		for offset := 1; offset <= len(choices); offset++ {
			candidate := (index + offset) % len(choices)
			if strings.HasPrefix(choices[candidate], prefix) {
				next = candidate
				break
			}
		}
		if next == index {
			return false
		}
	}
	switch m.setting {
	case 0:
		m.settings.Model = choices[next]
	case 1:
		m.settings.Voice = choices[next]
	case 2:
		m.settings.Format = choices[next]
	}
	return true
}

func audioSettingText(key tea.KeyPressMsg) string {
	if key.Mod & ^tea.ModShift != 0 {
		return ""
	}
	if key.Text != "" {
		return key.Text
	}
	if key.Code >= ' ' && key.Code <= '~' {
		return string(key.Code)
	}
	return ""
}
