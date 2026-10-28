//go:build darwin || linux

package autocomplete

import (
	"errors"
	"os"
)

func makePickerRecoveryDirectory(root *os.Root, name string) error {
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	// Darwin ACL grants are independent of mode bits. Refuse inheritable
	// grants before creation; POSIX ACL inheritance is bounded by mode 0700.
	if err := checkPickerDirectoryPermissions(parent); err != nil {
		return err
	}
	return root.Mkdir(name, 0700)
}

func checkPickerRecoveryDirectoryPermissions(file *os.File, info os.FileInfo) error {
	if info.Mode().Perm() != 0700 {
		return errors.New("shell recovery directory must be private")
	}
	return checkPickerDirectoryPermissions(file)
}
