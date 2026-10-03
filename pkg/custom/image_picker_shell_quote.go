package custom

import (
	"fmt"
	"strings"
	"unicode"
)

func imagePickerQuoteProperties(value string) (plain, control bool) {
	plain = value != ""
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:-", r)) {
			plain = false
		}
		control = control || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029'
	}
	return
}

func imagePickerFishQuote(value string) string {
	if plain, _ := imagePickerQuoteProperties(value); plain {
		return value
	}
	// Fish joins adjacent quoted and escaped fragments into one argument. Its
	// single quotes escape backslashes and quotes, unlike POSIX single quotes.
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range value {
		switch {
		case r == '\\' || r == '\'':
			b.WriteByte('\\')
			b.WriteRune(r)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029':
			fmt.Fprintf(&b, "'\\U%08x'", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func imagePickerPowerShellQuote(value string) string {
	plain, control := imagePickerQuoteProperties(value)
	if plain {
		return value
	}
	if !control && !strings.ContainsAny(value, "‘’‚‛“”„‟") {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	// PowerShell also treats smart quotes as delimiters. Unicode escapes keep
	// them literal and keep control bytes out of terminal output.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '`' || r == '$' || r == '"':
			b.WriteByte('`')
			b.WriteRune(r)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' || r >= '\u2018' && r <= '\u201f':
			fmt.Fprintf(&b, "`u{%x}", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
