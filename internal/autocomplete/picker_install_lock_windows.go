//go:build windows

package autocomplete

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FileInfo has no Windows owner or DACL. The opened handle is checked by the
// directory, metadata, and lock helpers before it is trusted.
func pickerInstallOwned(os.FileInfo) bool { return true }

func createPickerInstallLock(root *os.Root, name string) (*os.File, error) {
	if !filepath.IsLocal(name) || filepath.Base(name) != name {
		return nil, errors.New("shell integration lock requires a local filename")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, errors.New("cannot inspect shell integration owner")
	}
	// Protect the new lock from inherited read grants at creation. Tightening
	// its DACL afterward cannot revoke a reader's existing locking handle.
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return nil, errors.New("cannot prepare shell integration lock permissions")
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(directory.Fd()), ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE, SecurityDescriptor: descriptor,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, &attributes, &windows.IO_STATUS_BLOCK{}, nil,
		windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			err = status.Errno()
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(handle), name), nil
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
