//go:build !darwin && !linux && !windows

package files

import "errors"

func fileReceiptGoCallerName(int) (string, error) {
	return "", errors.New("Go launcher parent detection is unavailable on this platform")
}
