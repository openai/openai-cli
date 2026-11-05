package clihelp

import (
	"os"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

// Format authored setup guides after template expansion. Preserve shell lines
// and insertion links; only prose wraps and odd indentation levels change.
func formatGuide(command *cli.Command, text string) string {
	style := commandHelpStyle(command)
	width := helpWidth(command)
	invocation := DisplayInvocation(command.Root())
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
				out.WriteByte('\n')
			}
			continue
		}
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		indent := strings.Repeat(" ", spaces-spaces%2)
		plain := ansi.Strip(trimmed)
		if spaces >= 6 || strings.HasPrefix(plain, invocation+" ") {
			out.WriteString(indent + trimmed + "\n")
			continue
		}
		wrapped := strings.TrimSuffix(wrapPlainDescription(trimmed, indent, width), "\n")
		switch {
		case spaces == 0 && (out.Len() == 0 || strings.ToUpper(plain) == plain && strings.IndexFunc(plain, unicode.IsLetter) >= 0):
			wrapped = style.render(style.section, wrapped)
		case strings.HasSuffix(plain, ":"):
			wrapped = style.render(style.note, wrapped)
		}
		out.WriteString(wrapped)
		out.WriteByte('\n')
	}
	output := command.Root().Writer
	if output == nil {
		output = os.Stdout
	}
	if out.Len() > 0 && welcomeTerminal(output) && !strings.HasSuffix(out.String(), "\n\n") {
		out.WriteByte('\n')
	}
	return out.String()
}
