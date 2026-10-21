//go:build windows

package windowsfile

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OpenNoFollow opens an existing writable leaf without following reparse points.
// Parent traversal retains normal Windows semantics and requires no directory
// listing permission. No create, truncate, or permission-changing flags are used.
func OpenNoFollow(path string) (*os.File, error) {
	dosName, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	if err := freeUnicodeString.Find(); err != nil {
		return nil, err
	}
	var objectName windows.NTUnicodeString
	if err := windows.RtlDosPathNameToNtPathName(dosName, &objectName, nil, nil); err != nil {
		return nil, err
	}
	defer freeUnicodeString.Call(uintptr(unsafe.Pointer(&objectName)))
	attributes := windows.OBJECT_ATTRIBUTES{ObjectName: &objectName, Attributes: windows.OBJ_CASE_INSENSITIVE}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	// FileAttributeTagInfo requires FILE_READ_ATTRIBUTES independently of write
	// access. Without it, the safety query rejects ordinary existing files.
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES, &attributes, &windows.IO_STATUS_BLOCK{}, nil,
		windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			err = status.Errno()
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	fileType, err := windows.GetFileType(handle)
	if err == nil && fileType == windows.FILE_TYPE_DISK {
		var info struct{ attributes, tag uint32 }
		err = windows.GetFileInformationByHandleEx(handle, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		// IsReparseTagNameSurrogate marks links to another named entity,
		// including unfamiliar redirecting tags. Non-surrogate metadata such
		// as Windows deduplication must retain ordinary file behavior.
		if err == nil && info.attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && info.tag&0x20000000 != 0 {
			err = windows.ERROR_CANT_ACCESS_FILE
		}
	}
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
