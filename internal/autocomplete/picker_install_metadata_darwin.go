//go:build darwin

package autocomplete

import (
	"errors"
	"os"

	"github.com/openai/openai-cli/internal/darwinacl"
	"golang.org/x/sys/unix"
)

// Darwin metadata checks already refuse all attributes on both files.
func checkPickerReplacementLabels(*os.File, *os.File) error { return nil }

// Replacing a startup file with a new inode must not discard its extended
// attributes, ACL, or BSD flags. Leave these profiles for manual configuration.
func checkPickerFileMetadata(file *os.File) error {
	if err := checkPickerFileMode(file); err != nil {
		return err
	}
	if err := checkPickerFileLinks(file); err != nil {
		return err
	}
	count, err := unix.Flistxattr(int(file.Fd()), nil)
	if err != nil || count != 0 {
		return errors.New("shell startup file has protected or unreadable extended metadata")
	}
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil || info.Flags != 0 {
		return errors.New("shell startup file has protected or unreadable file flags")
	}
	if darwinacl.Check(file, darwinacl.Absent) != nil {
		return errors.New("shell startup file has protected or unreadable access permissions")
	}
	return nil
}

// Darwin ACLs can grant writes without changing POSIX mode bits. A profile's
// immediate parent may have the standard home-directory deny-delete ACL, so
// accept deny-only ACLs while conservatively refusing any grant or unknown form.
func checkPickerDirectoryPermissions(file *os.File) error {
	if darwinacl.Check(file, darwinacl.DenyOnly) != nil {
		return errors.New("shell directory has protected or unreadable access permissions")
	}
	return nil
}

// The ancestry check already rejects every Darwin ACL permission grant.
func checkPickerChildCreationPermissions(*os.File) error { return nil }
