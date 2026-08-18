//go:build windows

package cmd

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createReplaySnapshotFile() (*os.File, error) {
	placeholder, err := os.CreateTemp("", "openai-multipart-upload-*")
	if err != nil {
		return nil, err
	}
	path := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	securityAttributes, err := replaySnapshotSecurityAttributes()
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_DELETE,
		securityAttributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_TEMPORARY|windows.FILE_FLAG_DELETE_ON_CLOSE,
		0,
	)
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	return os.NewFile(uintptr(handle), path), nil
}

func replaySnapshotSecurityAttributes() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get current user for multipart replay snapshot: %w", err)
	}
	userSID := user.User.Sid.String()
	if userSID == "" {
		return nil, errors.New("get current user SID for multipart replay snapshot")
	}
	securityDescriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;GA;;;" + userSID + ")",
	)
	if err != nil {
		return nil, fmt.Errorf("create multipart replay snapshot security descriptor: %w", err)
	}
	return &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: securityDescriptor,
	}, nil
}

func duplicateFile(file *os.File) (*os.File, error) {
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(
		process,
		windows.Handle(file.Fd()),
		process,
		&duplicate,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(duplicate), file.Name()), nil
}
