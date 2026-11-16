//go:build windows

package updatenotice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Exclude writers before inspecting bytes. Allow the verified handle to acquire
// DELETE access later, after the Internet zone has been preserved by name.
func openActivationImage(root *os.Root) (*os.File, error) {
	return openWindowsStagedImage(root, windows.FILE_GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE)
}

// Pin the same file against both writes and renames. The final hash and the
// atomic rename both use this handle. A busy scanner leaves the update staged.
func pinActivationImage(ctx context.Context, root *os.Root, _ *os.File, before os.FileInfo, hash string) (*os.File, os.FileInfo, error) {
	file, err := openWindowsStagedImage(root, windows.FILE_GENERIC_READ|windows.DELETE, windows.FILE_SHARE_READ)
	if err != nil {
		return nil, nil, err
	}
	info, err := preparedImageAfterTrust(ctx, file, before, hash)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

// Open only the fixed leaf in the already verified cache directory.
func openWindowsStagedImage(root *os.Root, access, sharing uint32) (*os.File, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	name, err := windows.NewNTUnicodeString("next")
	if err != nil {
		return nil, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(dir.Fd()), ObjectName: name,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, access, &attributes, &windows.IO_STATUS_BLOCK{}, nil,
		windows.FILE_ATTRIBUTE_NORMAL, sharing, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			err = status.Errno()
		}
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_USER_MAPPED_FILE) {
			return nil, errors.Join(errBusyUpdateImage, err)
		}
		return nil, err
	}
	file := os.NewFile(uintptr(handle), filepath.Join(root.Name(), "next"))
	if !safeOpenedUpdateFile(file) {
		file.Close()
		return nil, errCache
	}
	return file, nil
}

func renameActivationImage(root *os.Root, verified *os.File, target string) error {
	const installedName = "openai.exe"
	if target != installedName {
		return errCache
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	// This is FILE_RENAME_INFORMATION_EX with the fixed native filename.
	// Match Go's os.Root.Rename replacement and older-filesystem fallback.
	info := struct {
		flags     uint32
		directory windows.Handle
		length    uint32
		name      [len(installedName)]uint16
	}{
		flags:     windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS,
		directory: windows.Handle(directory.Fd()), length: uint32(2 * len(installedName)),
	}
	for i, char := range installedName {
		info.name[i] = uint16(char)
	}
	const fileRenameInformationEx = 65
	handle := windows.Handle(verified.Fd())
	buf := (*byte)(unsafe.Pointer(&info))
	size := uint32(unsafe.Sizeof(info))
	err = windows.NtSetInformationFile(handle, &windows.IO_STATUS_BLOCK{}, buf, size, fileRenameInformationEx)
	if err == nil {
		return nil
	}
	info.flags = windows.FILE_RENAME_REPLACE_IF_EXISTS
	err = windows.NtSetInformationFile(handle, &windows.IO_STATUS_BLOCK{}, buf, size, windows.FileRenameInformation)
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
