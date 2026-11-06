//go:build !darwin && !linux && !windows

package terminal

import "errors"

// ParentProcessName returns the immediate parent process name.
func ParentProcessName() (string, error) {
	return "", errors.New("parent shell detection is unavailable on this platform")
}
