package codex

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// The terminal guide only changes presentation. It never reads input, queries
// terminal colors, starts a program, or changes the user's configuration.
func writeCodexTerminalGuide(ctx context.Context, command *cli.Command, guide codexInstructions) (bool, error) {
	root := command.Root()
	output, terminal := root.Writer.(*os.File)
	if !terminal || !cliterminal.AutomaticPresentation(root.String("format"), term.IsTerminal(output.Fd()), os.Getenv) {
		return false, nil
	}
	width, _, err := term.GetSize(output.Fd())
	if err != nil || width < 40 {
		return false, nil
	}
	return true, writeCodexTerminalContent(ctx, output, cliterminal.ColorProfile(output, os.Environ(), term.IsTerminal(output.Fd())), guide, width, cliterminal.DarkBackground(os.Getenv("COLORFGBG")))
}

func writeCodexTerminalContent(ctx context.Context, output io.Writer, profile colorprofile.Profile, guide codexInstructions, width int, dark bool) error {
	writer := &colorprofile.Writer{Forward: clioutput.Writer{Context: ctx, Sink: output}, Profile: profile}
	_, err := io.WriteString(writer, renderCodexTerminalGuide(guide, width, dark))
	return err
}

func renderCodexTerminalGuide(guide codexInstructions, width int, dark bool) string {
	width = max(40, min(width, 80))
	focus, muted, border := "#3159BC", "#657087", "#BAC4D8"
	if dark {
		focus, muted, border = "#8AA8FF", "#A4ACC2", "#51566B"
	}
	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(focus))
	note := lipgloss.NewStyle().Foreground(lipgloss.Color(muted))
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(border)).Padding(0, 1).Width(width - 2)
	var out strings.Builder
	fmt.Fprintln(&out, frame.Render(heading.Render("Codex")+"\n"+"A separate CLI named codex."))
	paragraph := func(text string) {
		fmt.Fprintln(&out, ansi.Wordwrap(readable.Text(text), width, ""))
	}
	section := func(label string) { fmt.Fprintln(&out, "\n"+heading.Render(label)) }
	command := func(text string) { fmt.Fprintln(&out, "  "+readable.Text(text)) }
	section("Install · choose one")
	paragraph("Use a package manager that is already installed.")
	for _, installation := range guide.Installation {
		fmt.Fprintln(&out, note.Render(readable.Text(installation.Method)))
		command(installation.Command)
	}
	section("Start")
	paragraph("Run from your project directory:")
	command(guide.Command)
	paragraph("Codex CLI handles its own sign-in.")
	section("Configure")
	paragraph("User: " + guide.DefaultConfig)
	paragraph("Trusted project: " + guide.ProjectConfig)
	paragraph("CODEX_HOME can change the user configuration directory.")
	for _, line := range strings.Split(guide.ConfigExample, "\n") {
		command(line)
	}
	section("Official links")
	for _, destination := range guide.Destinations {
		fmt.Fprintln(&out, note.Render(readable.Text(destination.Destination)))
		// Keep URLs intact for copying. Terminals naturally wrap long links;
		// inserted hard newlines would change their copied bytes.
		fmt.Fprintln(&out, readable.Text(destination.URL))
	}
	fmt.Fprintln(&out)
	paragraph("Web destinations retain their own sign-in and access requirements.")
	paragraph("This guide installs nothing and changes no settings.")
	return out.String()
}
