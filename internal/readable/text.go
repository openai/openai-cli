package readable

import (
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/jsonview"
)

// jsonview covers terminal C0/C1 controls. Directional and line-separator
// characters also need escaping because they can visually reorder labels.
var directionalControls = strings.NewReplacer(
	"\u061c", `\u061c`, "\u200e", `\u200e`, "\u200f", `\u200f`,
	"\u2028", `\u2028`, "\u2029", `\u2029`, "\u202a", `\u202a`,
	"\u202b", `\u202b`, "\u202c", `\u202c`, "\u202d", `\u202d`,
	"\u202e", `\u202e`, "\u2066", `\u2066`, "\u2067", `\u2067`,
	"\u2068", `\u2068`, "\u2069", `\u2069`,
)

func sanitize(text string) string {
	return directionalControls.Replace(jsonview.SanitizeTerminalString(text))
}

// Text escapes terminal controls and directional marks while preserving ordinary
// newlines and tabs. It adds no newline and does not redact sensitive content.
func Text(text string) string {
	if !strings.ContainsAny(text, "\n\t") {
		return sanitize(text)
	}
	var b strings.Builder
	for {
		i := strings.IndexAny(text, "\n\t")
		if i < 0 {
			b.WriteString(sanitize(text))
			return b.String()
		}
		b.WriteString(sanitize(text[:i]))
		b.WriteByte(text[i])
		text = text[i+1:]
	}
}

// WriteText escapes terminal controls and writes one complete text result.
// It appends a newline only when the text does not already end with one.
func WriteText(out io.Writer, value string) error {
	text := Text(value)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	n, err := io.WriteString(out, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return err
}
