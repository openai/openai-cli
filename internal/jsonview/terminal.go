package jsonview

import (
	"fmt"
	"strings"
)

func sanitizeTerminalString(s string) string {
	var b strings.Builder
	for _, r := range s {
		escaped := terminalControlEscape(r)
		if escaped == "" {
			b.WriteRune(r)
			continue
		}
		b.WriteString(escaped)
	}
	if b.Len() == len(s) {
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
