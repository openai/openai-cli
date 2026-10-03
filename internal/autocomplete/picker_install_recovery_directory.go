package autocomplete

import (
	"errors"
	"os"
	"path/filepath"
)

func createPickerRecoveryDirectory(root *os.Root, name string) error {
	if !filepath.IsLocal(name) || name == "." || filepath.Base(name) != name {
		return os.ErrInvalid
	}
	return makePickerRecoveryDirectory(root, name)
}

func checkPickerRecoveryDirectory(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() || !pickerInstallOwned(info) {
		return errors.New("shell recovery directory must be owned by the current user")
	}
	return checkPickerRecoveryDirectoryPermissions(file, info)
}
