//go:build !darwin && !linux && !windows

package custom

import "errors"

func imagePickerParentProcessName() (string, error) {
	return "", errors.New("parent shell detection is unavailable on this platform")
}
