package listview

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/openai/openai-cli/internal/terminal"
)

// Style decorates resource labels and list controls after layout. Its zero value preserves plain output.
type Style struct {
	profile colorprofile.Profile
	accent  lipgloss.Style
	muted   lipgloss.Style
}

// NewStyle uses the same normal-weight blue and secondary colors as command help.
func NewStyle(profile colorprofile.Profile, dark bool) Style {
	accent, muted := "#3159BC", "#657087"
	if dark {
		accent, muted = "#8AA8FF", "#A4ACC2"
	}
	return Style{profile: profile,
		accent: lipgloss.NewStyle().Foreground(lipgloss.Color(accent)),
		muted:  lipgloss.NewStyle().Foreground(lipgloss.Color(muted)),
	}
}

// NewFieldStyle uses the terminal's foreground when its background is unknown.
func NewFieldStyle(profile colorprofile.Profile, hint string) Style {
	if dark, known := terminal.BackgroundHint(hint); known {
		return NewStyle(profile, dark)
	}
	return Style{profile: profile, accent: lipgloss.NewStyle().Bold(true)}
}

// Table colors only the header of a nonempty table, preserving every data row.
func (s Style) Table(content string) string {
	header, rest, found := strings.Cut(content, "\n")
	if !found {
		return s.Label(header)
	}
	return s.Label(header) + "\n" + rest
}

// Label applies the command-help accent to a field label or table header.
func (s Style) Label(label string) string { return s.render(s.accent, label) }

// Note colors secondary text without changing its whitespace.
func (s Style) Note(content string) string {
	return s.render(s.muted, content)
}

// controls receives only fixed UI hints, already truncated to the terminal width.
func (s Style) controls(content string) string {
	words := strings.Split(content, " ")
	for i, word := range words {
		style := s.muted
		if strings.HasSuffix(word, ":") {
			style = s.accent
		}
		words[i] = s.render(style, word)
	}
	return strings.Join(words, " ")
}

func (s Style) render(style lipgloss.Style, content string) string {
	if s.profile <= colorprofile.ASCII || content == "" {
		return content
	}
	var out strings.Builder
	writer := colorprofile.Writer{Forward: &out, Profile: s.profile}
	for i, line := range strings.Split(content, "\n") {
		if i > 0 {
			_, _ = writer.WriteString("\n")
		}
		_, _ = writer.WriteString(style.Render(line))
	}
	return out.String()
}
