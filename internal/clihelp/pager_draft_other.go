//go:build !unix

package clihelp

import "os"

func openHelpDraft(_, _ *os.File) *helpDraft { return nil }
