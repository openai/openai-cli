package terminal

import (
	"fmt"
	"strings"
	"unicode"
)

// ShellQuoter returns the argument quoter for a supported shell, or nil.
func ShellQuoter(shell string) func(string) string {
	switch shell {
	case "bash", "zsh":
		return ShellQuote
	case "fish":
		return FishQuote
	case "pwsh":
		return PowerShellQuote
	default:
		return nil
	}
}

// FormatCommand formats an openai invocation. An empty result means the shell is unsupported.
func FormatCommand(args []string, shell string) string {
	quote := ShellQuoter(shell)
	if quote == nil {
		return ""
	}
	words := make([]string, 0, len(args)+1)
	words = append(words, "openai")
	for _, arg := range args {
		words = append(words, quote(arg))
	}
	return strings.Join(words, " ")
}

// ShellQuote quotes one argument for Bash or zsh.
func ShellQuote(value string) string {
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
