//go:build !unix

package clihelp

import "os"

// The status-response handoff uses Unix tty byte input. Other platforms
// still disable mouse reporting before the reader shuts down.
func finishHelpMouseInput(_, _ *os.File, _ *helpDraft, _ bool) (bool, error) { return false, nil }
