//go:build windows

// Package windowsfile handles private creation and non-following Windows file opens.
package windowsfile

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Callers translate these setup failures into their own diagnostics.
var (
	ErrInvalidName = errors.New("private file requires a local filename")
	ErrOwner       = errors.New("cannot inspect private file owner")
	ErrPermissions = errors.New("cannot prepare private file permissions")
)

// CreatePrivate creates one leaf in root, without replacing an existing file.
// The protected DACL grants full access to the current user, SYSTEM, and Administrators.
// Set it at creation: later changes cannot revoke access through existing handles.
func CreatePrivate(root *os.Root, name string) (*os.File, error) {
	if !filepath.IsLocal(name) || filepath.Base(name) != name {
		return nil, ErrInvalidName
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, ErrOwner
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return nil, ErrPermissions
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

var freeUnicodeString = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlFreeUnicodeString")

// CreatePrivateDirectory creates path with a protected, inheritable private DACL.
// It preserves parent traversal without requiring permission to list the parent.
func CreatePrivateDirectory(path string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, ErrOwner
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return nil, ErrPermissions
	}
	dosName, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	if err := freeUnicodeString.Find(); err != nil {
		return nil, err
	}
	// Let Windows resolve DOS, relative, UNC, and extended paths itself.
	var objectName windows.NTUnicodeString
	if err := windows.RtlDosPathNameToNtPathName(dosName, &objectName, nil, nil); err != nil {
		return nil, err
	}
	defer freeUnicodeString.Call(uintptr(unsafe.Pointer(&objectName)))
	attributes := windows.OBJECT_ATTRIBUTES{
		ObjectName: &objectName, Attributes: windows.OBJ_CASE_INSENSITIVE, SecurityDescriptor: descriptor,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE, &attributes, &windows.IO_STATUS_BLOCK{}, nil,
		windows.FILE_ATTRIBUTE_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			err = status.Errno()
		}
		return nil, &os.PathError{Op: "mkdir", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
