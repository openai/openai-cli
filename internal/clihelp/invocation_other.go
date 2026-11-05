//go:build !windows

package clihelp

import "strings"

func quoteInvocation(name, fallback string) string {
	return "'" + strings.ReplaceAll(name, "'", "'\\''") + "'"
}
