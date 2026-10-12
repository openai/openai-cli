//go:build !darwin && !linux && !windows

package custom

import "errors"

func fileReceiptGoCallerName(int) (string, error) {
	return "", errors.New("Go launcher parent detection is unavailable on this platform")
}
