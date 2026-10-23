package clihelp

import (
	"os"
	"runtime"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

// Help uses environment-only detection: it must not query stdin or launch tmux.
func commandHelpStyle(command *cli.Command) helpStyle {
	root := command.Root()
	output := root.Writer
	if output == nil {
		output = os.Stdout
	}
	profile := colorprofile.NoTTY
	format := root.String("format")
	if (format == "" || strings.EqualFold(format, "auto") || strings.EqualFold(format, "text")) &&
		welcomeTerminal(output) && welcomeEnvironment(os.Getenv, runtime.GOOS) &&
		os.Getenv("NO_COLOR") == "" && os.Getenv("FORCE_COLOR") != "0" && os.Getenv("CLICOLOR") != "0" {
		profile = colorprofile.Env(os.Environ())
		if runtime.GOOS == "windows" && os.Getenv("TERM") == "" && os.Getenv("WT_SESSION") == "" {
			profile = colorprofile.NoTTY
		}
	}
	return newHelpStyle(profile)
}

type helpStyle struct {
	profile colorprofile.Profile
	section lipgloss.Style
	note    lipgloss.Style
	value   lipgloss.Style
}

func newHelpStyle(profile colorprofile.Profile) helpStyle {
	accent, muted := "#3159BC", "#657087"
	if welcomeDarkBackground(os.Getenv("COLORFGBG")) {
		accent, muted = "#8AA8FF", "#A4ACC2"
	}
	return helpStyle{
		profile: profile,
		section: lipgloss.NewStyle().Foreground(lipgloss.Color(accent)),
		note:    lipgloss.NewStyle().Foreground(lipgloss.Color(muted)),
		value:   lipgloss.NewStyle().Foreground(lipgloss.Color(muted)),
	}
}

// Decorate after layout so escapes cannot affect wrapping or command padding.
// Return plain output directly to preserve whitespace and insertion links.
func (s helpStyle) render(style lipgloss.Style, text string) string {
	if s.profile <= colorprofile.ASCII || text == "" {
		return text
	}
	var out strings.Builder
	writer := colorprofile.Writer{Forward: &out, Profile: s.profile}
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			_, _ = writer.WriteString("\n")
		}
		_, _ = writer.WriteString(style.Render(line))
	}
	return out.String()
}

// Use one column for a whole list. Keep at least 36 cells for descriptions;
// otherwise stack every entry instead of making individual rows change shape.
func helpDescriptionColumn(width int, names []string) int {
	column := 26
	for _, name := range names {
		column = max(column, 2+ansi.StringWidth(name)+2)
	}
	if width < 72 || width-column < 36 {
		return 0
	}
	return column
}

func (s helpStyle) entry(name, description, label string, width, column int) string {
	// Keep command tokens and insertion links intact when columns cannot fit.
	if column == 0 {
		return "  " + label + "\n" + s.render(s.note, wrapEntryDescription(description, "    ", width))
	}
	indent := strings.Repeat(" ", column)
	details := wrapEntryDescription(description, indent, width)
	if details == "" {
		return "  " + label + "\n"
	}
	return "  " + label + strings.Repeat(" ", column-2-ansi.StringWidth(name)) + s.render(s.note, strings.TrimPrefix(details, indent))
}

// Avoid leaving a single word on the final line of a command summary.
func wrapEntryDescription(description, indent string, width int) string {
	details := wrapDescription(description, indent, width)
	lines := strings.Split(strings.TrimSuffix(details, "\n"), "\n")
	last := len(lines) - 1
	if last > 0 && len(strings.Fields(lines[last])) == 1 {
		previous := strings.TrimPrefix(lines[last-1], indent)
		if split := strings.LastIndexByte(previous, ' '); split > 0 {
			ending := indent + previous[split+1:] + " " + strings.TrimSpace(lines[last])
			if ansi.StringWidth(ending) <= width {
				lines[last-1] = indent + strings.TrimRight(previous[:split], " ")
				lines[last] = ending
				return strings.Join(lines, "\n") + "\n"
			}
		}
	}
	return details
}

func (s helpStyle) flag(name, value string) string {
	if value != "" {
		name += "=" + s.render(s.value, value)
	}
	return name
}
