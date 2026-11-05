//go:build windows

package profiles

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func openFlags() int { return 0 }

// Windows permissions inherit the user's configuration-directory ACL.
// Unix permission bits cannot validate that ACL.
func privatePermissions(os.FileInfo) bool { return true }
