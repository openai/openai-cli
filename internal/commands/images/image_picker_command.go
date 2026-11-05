package images

import (
	"strings"

	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

func (s imagePickerSettings) args() []string {
	prompt := s.prompt
	if strings.HasPrefix(prompt, "@") {
		// The existing request parser expands @file arguments. User text entered
		// into the picker is always literal, including file-looking strings.
		prompt = `\` + prompt
	}
	// Keep settings ahead of potentially long prompt text so live previews show
	// setting changes even when the prompt tail needs to be truncated.
	args := []string{"images", "generate", "--model", s.model, "--size", s.size,
		"--quality", s.quality, "--output-format", s.format, "--background", s.background, "--count", s.count}
	if s.outputDir != "" {
		args = append(args, "--output-dir", s.outputDir)
	}
	return append(args, "--prompt", prompt)
}

const imagePickerUnsupportedShell = "Command printing needs Bash, zsh, fish or PowerShell 7."

type imagePickerConnection struct {
	invocation string
	args       []string
	unsafe     bool
}

// Connection options belong in the displayed command only. The current request already has them.
func (p imagePickerConnection) display(args []string, shell string) (command, unavailable string) {
	if why := p.unavailable(shell); why != "" {
		return "", why
	}
	args = append(p.args[:len(p.args):len(p.args)], args...)
	command = cliterminal.FormatCommand(args, shell)
	if p.invocation != "" {
		command = p.invocation + strings.TrimPrefix(command, "openai")
	}
	return command, ""
}

func (p imagePickerConnection) unavailable(shell string) string {
	if p.unsafe {
		return "Command hidden because it requires private request options."
	}
	if cliterminal.ShellQuoter(shell) == nil {
		return imagePickerUnsupportedShell
	}
	return ""
}
