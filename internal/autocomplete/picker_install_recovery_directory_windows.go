//go:build windows

package autocomplete

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func makePickerRecoveryDirectory(root *os.Root, name string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("cannot inspect shell recovery owner")
	}
	// Set the protected DACL at creation, before an inherited reader could
	// open the directory and keep a handle to retained profile contents.
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return errors.New("cannot prepare shell recovery permissions")
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(directory.Fd()), ObjectName: objectName,
		Attributes: windows.OBJ_DONT_REPARSE, SecurityDescriptor: descriptor,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, &attributes, &windows.IO_STATUS_BLOCK{}, nil,
		windows.FILE_ATTRIBUTE_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return &os.PathError{Op: "mkdir", Path: name, Err: pickerRenameWindowsError(err)}
	}
	return windows.CloseHandle(handle)
}

func checkPickerRecoveryDirectoryPermissions(file *os.File, _ os.FileInfo) error {
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &attributes); err != nil {
		return err
	}
	if attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("shell recovery directory must not be a Windows reparse point")
	}
	return checkPickerWindowsPermissions(file, true)
}
