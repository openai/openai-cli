package jsonview

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// SanitizeTerminalString escapes control characters that terminals can interpret.
func SanitizeTerminalString(s string) string {
	var b strings.Builder
	for i, r := range s {
		escaped := terminalControlEscape(r)
		if b.Cap() == 0 && escaped == "" {
			if r != utf8.RuneError {
				continue
			}
			// Preserve range's existing normalization of malformed UTF-8.
			_, width := utf8.DecodeRuneInString(s[i:])
			if width != 1 {
				continue
			}
		}
		if b.Cap() == 0 {
			b.Grow(len(s))
			b.WriteString(s[:i])
		}
		if escaped == "" {
			b.WriteRune(r)
		} else {
			b.WriteString(escaped)
		}
	}
	if b.Cap() == 0 {
		return s
	}
	return b.String()
}

func terminalControlEscape(r rune) string {
	switch r {
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	}
	// Escape remaining C0 controls, DEL, and C1 controls. These ranges include
	// ESC, BEL, and 8-bit CSI, which terminals can interpret as control
	// sequences when printed directly.
	if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
		return fmt.Sprintf(`\u%04x`, r)
	}
	return ""
}
