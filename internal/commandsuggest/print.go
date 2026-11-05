package commandsuggest

import (
	"io"

	"github.com/urfave/cli/v3"
)

// WriteLine displays one explicitly requested command. Failed writes invalidate
// capture, including when the optional link consumes more bytes.
func WriteLine(command *cli.Command, output io.Writer, text string) error {
	if command != nil {
		if capture, ok := command.Root().Metadata[metadataKey].(*collector); ok && capture != nil && validCommand(text) && capture.terminal(output) {
			capture.mu.Lock()
			defer capture.mu.Unlock()
			rendered := capture.renderLocked(text)
			err := writeLine(output, rendered)
			if err != nil {
				// A partial write can expose a link. Invalidate this entire capture
				// so a later command cannot reuse that identifier.
				capture.failed = true
			}
			return err
		}
	}
	if err := writeLine(output, text); err != nil {
		return err
	}
	RecordTo(command, output, text)
	return nil
}

func writeLine(output io.Writer, text string) error {
	text += "\n"
	n, err := io.WriteString(output, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return err
}

// Invalidate discards this invocation's suggestions after a failed presentation.
// Already emitted links must never resolve through a partial command capture.
func Invalidate(command *cli.Command) {
	if command == nil {
		return
	}
	if capture, ok := command.Root().Metadata[metadataKey].(interface{ Invalidate() }); ok {
		capture.Invalidate()
	}
}

func (c *collector) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed = true
}
