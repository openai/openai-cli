//go:build !darwin && !linux && !windows

package autocomplete

import (
	"errors"
	"os"
)

func makePickerRecoveryDirectory(*os.Root, string) error {
	return errors.New("safe shell startup recovery is unsupported on this platform")
}

func checkPickerRecoveryDirectoryPermissions(*os.File, os.FileInfo) error {
	return errors.New("safe shell startup recovery is unsupported on this platform")
}
