package images

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Images uses the Batch palette. Ordinary text and the canvas inherit the terminal.
type imagePickerPalette struct {
	accent, fill, selected, secondary, border, success, warning, failure string
}

func (m *imagePicker) palette() imagePickerPalette {
	if m.dark {
		return imagePickerPalette{"#8AA8FF", "#343D58", "#F2F4FA", "#A4ACC2", "#51566B", "#8DC9A0", "#E2BC79", "#E59A9A"}
	}
	return imagePickerPalette{"#3159BC", "#E9EFFE", "#20283B", "#657087", "#BAC4D8", "#236B43", "#885B14", "#B23A3A"}
}

// Style UI text for the destination. Image pixels and native graphics bypass this path.
func (m *imagePicker) foreground(hex string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if m.color && m.profile >= colorprofile.ANSI {
		return style.Foreground(m.profile.Convert(lipgloss.Color(hex)))
	}
	return style
}

type imagePickerTone string

const (
	imageSecondary imagePickerTone = ""
	imageActive    imagePickerTone = "active"
	imageSuccess   imagePickerTone = "success"
	imageWarning   imagePickerTone = "warning"
	imageFailure   imagePickerTone = "failure"
)

func (m *imagePicker) setNote(text string, tone imagePickerTone) {
	m.note, m.noteTone = text, tone
}

func (m *imagePicker) workspaceText(text string, tone imagePickerTone) string {
	p := m.palette()
	hex := p.secondary
	switch tone {
	case imageActive:
		hex = p.accent
	case imageSuccess:
		hex = p.success
	case imageWarning:
		hex = p.warning
	case imageFailure:
		hex = p.failure
	}
	return m.foreground(hex).Render(text)
}
