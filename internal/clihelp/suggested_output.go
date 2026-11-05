package clihelp

import (
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

// An output is supplied only by the human-readable presenter. Formatting an
// error for JSON or a diagnostic comparison must not register an action.
func SuggestedCommand(command *cli.Command, outputs []io.Writer, text string) string {
	if len(outputs) == 0 {
		return text
	}
	if presenter, ok := outputs[0].(*SuggestedText); ok {
		presenter.commands = append(presenter.commands, text)
		return text
	}
	return commandsuggest.RecordTo(command, outputs[0], text)
}

// Automatically derived paths must consist of literal shell words. Explicit
// examples use SuggestedCommand and retain their author's intended quoting.
func SuggestedCommandPath(command *cli.Command, outputs []io.Writer, text string) string {
	if !ShellSafeCommandPath(command) {
		return text
	}
	return SuggestedCommand(command, outputs, text)
}

// Escape the complete readable result before adding trusted insertion links.
// The writer carries only explicitly declared commands, never discovered prose.
type SuggestedText struct {
	io.Writer
	command     *cli.Command
	destination io.Writer
	commands    []string
}

// NewSuggestedText prepares readable output containing explicitly declared suggestions.
func NewSuggestedText(out io.Writer, command *cli.Command) *SuggestedText {
	return NewSuggestedTextTo(out, out, command)
}

// NewSuggestedTextTo separates a wrapped writer from its terminal destination.
func NewSuggestedTextTo(out, destination io.Writer, command *cli.Command) *SuggestedText {
	return &SuggestedText{Writer: out, destination: destination, command: command}
}

// WriteText escapes text before rendering trusted insertion links.
func (p *SuggestedText) WriteText(text string) error {
	text = readable.Text(text)
	text = commandsuggest.RenderText(text, p.commands, func(command string) string {
		return commandsuggest.RecordTo(p.command, p.destination, command)
	})
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	n, err := io.WriteString(p.Writer, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	if err != nil {
		commandsuggest.Invalidate(p.command)
	}
	return err
}
