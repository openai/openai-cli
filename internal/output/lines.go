package output

import (
	"bytes"

	"github.com/muesli/reflow/wrap"
)

// CountTerminalLines counts wrapped newlines using the existing pager policy.
func CountTerminalLines(data []byte, terminalWidth int) int {
	return bytes.Count([]byte(wrap.String(string(data), terminalWidth)), []byte("\n"))
}
