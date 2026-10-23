//go:build !darwin && !linux && !windows

package terminal

import "errors"

func goCallerName(int) (string, error) {
	return "", errors.New("Go launcher parent detection is unavailable on this platform")
}
