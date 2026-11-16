package images

import "strings"

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
