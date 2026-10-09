//go:build windows

package autocomplete

import (
	"errors"
	"os"

	"github.com/openai/openai-cli/internal/windowsfile"
	"golang.org/x/sys/windows"
)

// FileInfo has no Windows owner or DACL. The opened handle is checked by the
// directory, metadata, and lock helpers before it is trusted.
func pickerInstallOwned(os.FileInfo) bool { return true }

func createPickerInstallLock(root *os.Root, name string) (*os.File, error) {
	file, err := windowsfile.CreatePrivate(root, name)
	switch {
	case errors.Is(err, windowsfile.ErrInvalidName):
		return nil, errors.New("shell integration lock requires a local filename")
	case errors.Is(err, windowsfile.ErrOwner):
		return nil, errors.New("cannot inspect shell integration owner")
	case errors.Is(err, windowsfile.ErrPermissions):
		return nil, errors.New("cannot prepare shell integration lock permissions")
	}
	return file, err
}

func tryPickerInstallLock(file *os.File) (bool, error) {
	// LockFileEx also accepts read handles, so lock files must deny foreign
	// readers as well as writers.
	if err := checkPickerWindowsPermissions(file, true); err != nil {
		return false, err
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	if err == nil {
		err = checkPickerWindowsPermissions(file, true)
	}
	return err == nil, err
}
