//go:build windows

package autocomplete

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FileRenameInformation uses a BOOLEAN followed by pointer-aligned fields.
// uint32 also accounts for the union with ULONG Flags on current Windows.
type pickerRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func renamePickerEntriesNoReplace(oldDirectory *os.File, oldname string, newDirectory *os.File, newname string) error {
	objectName, err := windows.NewNTUnicodeString(oldname)
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(oldDirectory.Fd()),
		ObjectName:    objectName,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var source windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&source, windows.DELETE|windows.SYNCHRONIZE, &attributes, &status,
		nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return pickerRenameWindowsError(err)
	}
	defer windows.CloseHandle(source)

	name, err := windows.UTF16FromString(newname)
	if err != nil {
		return err
	}
	nameBytes := (len(name) - 1) * 2
	var layout pickerRenameInformation
	buffer := make([]byte, int(unsafe.Sizeof(layout))+nameBytes)
	information := (*pickerRenameInformation)(unsafe.Pointer(&buffer[0]))
	// Leave ReplaceIfExists zero. Collision checks and the move must be one
	// filesystem operation; a preliminary Stat followed by Rename can lose data.
	information.RootDirectory = windows.Handle(newDirectory.Fd())
	information.FileNameLength = uint32(nameBytes)
	copy(unsafe.Slice(&information.FileName[0], len(name)-1), name[:len(name)-1])
	return pickerRenameWindowsError(windows.NtSetInformationFile(source, &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation))
}

func pickerRenameWindowsError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
