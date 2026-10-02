//go:build linux

package autocomplete

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Linux represents POSIX ACLs and security labels as extended attributes too.
// Do not drop any attribute through atomic replacement. This deliberately
// leaves profiles with SELinux labels or unknown metadata unchanged.
func checkPickerFileMetadata(file *os.File) error {
	count, err := unix.Flistxattr(int(file.Fd()), nil)
	if err != nil || count != 0 {
		return errors.New("shell startup file has protected or unreadable extended metadata")
	}
	return nil
}
