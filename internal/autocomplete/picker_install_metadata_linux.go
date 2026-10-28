//go:build linux

package autocomplete

import (
	"os"

	"golang.org/x/sys/unix"
)

// Accept kernel-assigned SELinux labels while retaining the refusal for ACLs
// and other attributes. Replacement separately compares the opened labels.
func checkPickerFileMetadata(file *os.File) error {
	if err := checkPickerFileMode(file); err != nil {
		return err
	}
	if err := checkPickerFileLinks(file); err != nil {
		return err
	}
	_, err := pickerLinuxSecurityLabel(file)
	return err
}

func pickerLinuxSecurityLabel(file *os.File) (pickerSecurityLabel, error) {
	return readPickerSecurityLabel(
		func(data []byte) (int, error) { return unix.Flistxattr(int(file.Fd()), data) },
		func(data []byte) (int, error) { return unix.Fgetxattr(int(file.Fd()), "security.selinux", data) },
	)
}

func checkPickerReplacementLabels(profile, staged *os.File) error {
	return comparePickerSecurityLabels(
		func() (pickerSecurityLabel, error) { return pickerLinuxSecurityLabel(profile) },
		func() (pickerSecurityLabel, error) { return pickerLinuxSecurityLabel(staged) },
	)
}
