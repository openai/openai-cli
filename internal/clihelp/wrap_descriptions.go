package clihelp

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/urfave/cli/v3"
)

func helpWidth(command *cli.Command) int {
	out := command.Root().Writer
	if out == nil {
		out = os.Stdout
	}
	if file, ok := out.(interface{ Fd() uintptr }); ok {
		if width, _, err := term.GetSize(file.Fd()); err == nil && width > 0 {
			return min(width, 100)
		}
	}
	return 80 // Stable text for redirected help and documentation.
}

// Wrap prose without splitting words, paths, flags or copyable commands.
func wrapDescription(text, indent string, width int) string {
	return wrapPlainDescription(terminalProse(text), indent, width)
}

// Already normalized prose must not reinterpret literal markup from code spans.
func wrapPlainDescription(text, indent string, width int) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	var fence descriptionFence
	for _, line := range reflowDescription(text) {
		if fence.contains(line) || isCodeLine(line) {
			out.WriteString(indent + line + "\n")
			continue
		}
		out.WriteString(indent)
		column := len(indent)
		leading := line[:len(line)-len(strings.TrimLeftFunc(line, unicode.IsSpace))]
		first := true
		for line != "" {
			spaces := line[:len(line)-len(strings.TrimLeftFunc(line, unicode.IsSpace))]
			line = line[len(spaces):]
			end := strings.IndexFunc(line, unicode.IsSpace)
			if end < 0 {
				end = len(line)
			}
			word := line[:end]
			line = line[end:]
			if !first && column+ansi.StringWidth(spaces+word) > width {
				out.WriteString("\n" + indent)
				column = len(indent)
				spaces = leading
			}
			out.WriteString(spaces)
			out.WriteString(word)
			column += ansi.StringWidth(spaces + word)
			first = false
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// Source comments often wrap prose at a fixed width. Join those continuation
// lines before laying out help, while retaining paragraphs, lists and code.
func reflowDescription(text string) []string {
	var lines []string
	var paragraph string
	flush := func() {
		if paragraph != "" {
			lines = append(lines, paragraph)
			paragraph = ""
		}
	}
	var fence descriptionFence
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		code := fence.contains(line) || isCodeLine(line)
		if trimmed == "" || code || strings.HasSuffix(trimmed, ":") {
			flush()
			lines = append(lines, line)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "• ") {
			flush()
		}
		if paragraph == "" {
			paragraph = line
		} else {
			paragraph += " " + trimmed
		}
	}
	flush()
	return lines
}

func isCodeLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") ||
		strings.HasPrefix(trimmed, "openai ") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "}")
}

func writeHelpEntry(out *strings.Builder, name, description string, width int) {
	if width < 72 || ansi.StringWidth(name) > 22 {
		fmt.Fprintf(out, "  %s\n", name)
		out.WriteString(wrapDescription(description, "    ", width))
		return
	}
	prefix := fmt.Sprintf("  %-22s ", name)
	wrapped := wrapDescription(description, strings.Repeat(" ", 25), width)
	if wrapped == "" {
		fmt.Fprintf(out, "  %s\n", name)
		return
	}
	out.WriteString(prefix + strings.TrimPrefix(wrapped, strings.Repeat(" ", 25)))
}
