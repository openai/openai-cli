package jsonview

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// SanitizeTerminalString escapes control characters that terminals can interpret.
func SanitizeTerminalString(s string) string {
	return sanitizeTerminalString(s, false)
}

func sanitizeTerminalString(s string, preserveLayout bool) string {
	var b strings.Builder
	for i, r := range s {
		escaped := terminalControlEscape(r)
		if preserveLayout && (r == '\n' || r == '\t') {
			escaped = ""
		}
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

// WriteTerminalText streams untrusted text with terminal controls escaped, keeping
// newlines and tabs for readable layout. Malformed UTF-8 becomes replacement
// characters, as in SanitizeTerminalString. It is only for terminal presentation;
// files, pipes, and explicitly requested raw output must retain their original bytes.
func WriteTerminalText(dst io.Writer, src io.Reader) error {
	var buffer [32 * 1024]byte
	pending := 0
	for {
		n, readErr := src.Read(buffer[pending:])
		data := buffer[:pending+n]
		end := len(data)
		if readErr == nil {
			// Keep at most one incomplete rune for the next read. Invalid UTF-8
			// is a complete RuneError and must be sanitized, not passed through.
			for start := max(0, end-utf8.UTFMax+1); start < end; start++ {
				if !utf8.FullRune(data[start:]) {
					end = start
					break
				}
			}
		}
		if end > 0 {
			text := sanitizeTerminalString(string(data[:end]), true)
			written, err := io.WriteString(dst, text)
			if err != nil {
				return err
			}
			if written != len(text) {
				return io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
		pending = copy(buffer[:], data[end:])
	}
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
