//go:build windows

package autocomplete

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func checkPickerFileMetadata(file *os.File) error {
	var streams [64 * 1024]byte
	if err := windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileStreamInfo, &streams[0], uint32(len(streams))); err != nil {
		return errors.New("cannot inspect shell startup file metadata")
	}
	return validatePickerFileStreams(streams[:])
}

// A replacement inherits the directory's permissions. Keep profiles with
// different file-specific permissions rather than changing their access rules.
// Query only owner/group/DACL; reading auditing metadata would need privileges.
func checkPickerReplacementMetadata(root *os.Root, name, temporary string, previous pickerFileSnapshot) error {
	if previous.info == nil {
		return nil
	}
	profile, err := root.Open(name)
	if err != nil {
		return errors.New("cannot inspect shell startup file permissions")
	}
	defer profile.Close()
	opened, err := profile.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(previous.info, opened) {
		return errPickerInstallChanged
	}
	stagedInfo, err := root.Lstat(temporary)
	if err != nil || !stagedInfo.Mode().IsRegular() {
		return errPickerInstallChanged
	}
	staged, err := root.Open(temporary)
	if err != nil {
		return errors.New("cannot inspect shell startup replacement permissions")
	}
	defer staged.Close()
	stagedOpened, err := staged.Stat()
	if err != nil || !os.SameFile(stagedInfo, stagedOpened) {
		return errPickerInstallChanged
	}
	const information = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
	existing, err := windows.GetSecurityInfo(windows.Handle(profile.Fd()), windows.SE_FILE_OBJECT, information)
	if err != nil {
		return errors.New("cannot inspect shell startup file permissions")
	}
	replacement, err := windows.GetSecurityInfo(windows.Handle(staged.Fd()), windows.SE_FILE_OBJECT, information)
	if err != nil {
		return errors.New("cannot inspect shell startup replacement permissions")
	}
	if err := comparePickerSecurityDescriptors(existing, replacement); err != nil {
		return err
	}
	for path, expected := range map[string]os.FileInfo{name: opened, temporary: stagedOpened} {
		current, err := root.Lstat(path)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
			return errPickerInstallChanged
		}
	}
	return nil
}

func comparePickerSecurityDescriptors(existing, replacement *windows.SECURITY_DESCRIPTOR) error {
	if existing == nil || replacement == nil || !existing.IsValid() || !replacement.IsValid() {
		return errors.New("cannot inspect shell startup file permissions")
	}
	existingControl, existingRevision, existingError := existing.Control()
	replacementControl, replacementRevision, replacementError := replacement.Control()
	if existingError != nil || replacementError != nil {
		return errors.New("cannot inspect shell startup file permissions")
	}
	for _, descriptor := range []*windows.SECURITY_DESCRIPTOR{existing, replacement} {
		owner, _, ownerError := descriptor.Owner()
		group, _, groupError := descriptor.Group()
		if ownerError != nil || groupError != nil || owner == nil || group == nil {
			return errors.New("cannot inspect shell startup file permissions")
		}
		if _, _, err := descriptor.DACL(); err != nil {
			return errors.New("cannot inspect shell startup file permissions")
		}
	}
	existingText, replacementText := existing.String(), replacement.String()
	if existingText == "" || replacementText == "" {
		return errors.New("cannot inspect shell startup file permissions")
	}
	if existingControl != replacementControl || existingRevision != replacementRevision || existingText != replacementText {
		return errors.New("shell startup file has different permissions; existing file was kept")
	}
	return nil
}
