//go:build !unix

package clihelp

import (
	"io"
	"os"
)

// Preserve the native console reader, including Windows console records.
func ownHelpProgramInput(_ *os.File, source io.Reader) (io.Reader, func()) {
	return source, func() {}
}

// The status-response handoff uses Unix tty byte input. Other platforms
// still disable mouse reporting before the reader shuts down.
func finishHelpMouseInput(_, _ *os.File, _ io.Writer, _ *helpDraft, _ bool) (bool, error) {
	return false, nil
}
