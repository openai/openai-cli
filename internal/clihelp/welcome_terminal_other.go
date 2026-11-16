//go:build !windows

package clihelp

func welcomePseudoTerminal(uintptr) bool { return false }
