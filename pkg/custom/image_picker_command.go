package custom

import (
	"fmt"
	"strings"
	"unicode"
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

func formatImagePickerCommand(args []string, shell string) string {
	words := make([]string, 0, len(args)+1)
	words = append(words, "openai")
	for _, arg := range args {
		quote := imagePickerShellQuote
		switch shell {
		case "fish":
			quote = imagePickerFishQuote
		case "pwsh":
			quote = imagePickerPowerShellQuote
		}
		words = append(words, quote(arg))
	}
	return strings.Join(words, " ")
}

func imagePickerShellQuote(value string) string {
	plain := value != ""
	escaped := false
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:-", r)) {
			plain = false
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			escaped = true
		}
	}
	if plain {
		return value
	}
	if !escaped {
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range value {
		switch {
		case r == '\\' || r == '\'':
			b.WriteByte('\\')
			b.WriteRune(r)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029':
			// Encode UTF-8 bytes, making C1 controls exact in both zsh and bash
			// regardless of each shell's treatment of Unicode escape syntax.
			for _, v := range []byte(string(r)) {
				fmt.Fprintf(&b, "\\x%02x", v)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
