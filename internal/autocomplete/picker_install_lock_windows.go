//go:build windows

package autocomplete

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Windows ownership/access is enforced by the filesystem ACL on each open.
func pickerInstallOwned(os.FileInfo) bool { return true }

func tryPickerInstallLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
