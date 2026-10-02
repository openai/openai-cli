//go:build darwin || linux

package autocomplete

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// The replacement inherits directory metadata. Refuse it if doing so would
// change a profile's owner/group or add protected attributes or access rules.
func checkPickerReplacementMetadata(root *os.Root, name, temporary string, previous pickerFileSnapshot) error {
	stagedIdentity, err := root.Lstat(temporary)
	if err != nil || !stagedIdentity.Mode().IsRegular() {
		return errPickerInstallChanged
	}
	staged, err := root.OpenFile(temporary, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer staged.Close()
	stagedOpened, err := staged.Stat()
	if err != nil || !os.SameFile(stagedIdentity, stagedOpened) {
		return errPickerInstallChanged
	}
	if err := checkPickerFileMetadata(staged); err != nil {
		return err
	}
	if previous.info == nil {
		current, err := root.Lstat(temporary)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(stagedOpened, current) {
			return errPickerInstallChanged
		}
		return nil
	}
	profile, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer profile.Close()
	opened, err := profile.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(previous.info, opened) {
		return errPickerInstallChanged
	}
	if err := checkPickerFileMetadata(profile); err != nil {
		return err
	}
	var existingInfo, stagedInfo unix.Stat_t
	if unix.Fstat(int(profile.Fd()), &existingInfo) != nil || unix.Fstat(int(staged.Fd()), &stagedInfo) != nil || existingInfo.Uid != stagedInfo.Uid || existingInfo.Gid != stagedInfo.Gid {
		return errors.New("shell startup replacement has different ownership; existing file was kept")
	}
	for path, expected := range map[string]os.FileInfo{name: opened, temporary: stagedOpened} {
		current, err := root.Lstat(path)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
			return errPickerInstallChanged
		}
	}
	return nil
}
