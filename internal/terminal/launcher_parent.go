package terminal

import "errors"

// GoCallerName returns the immediate caller of a Go launcher process.
// The caller must first verify that pid identifies its Go launcher.
// It does not search older ancestors or use shell configuration.
func GoCallerName(pid int) (string, error) {
	if pid <= 1 {
		return "", errors.New("Go launcher process is unavailable")
	}
	return goCallerName(pid)
}
