//go:build linux

package autocomplete

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func renamePickerEntriesNoReplace(oldDirectory *os.File, oldname string, newDirectory *os.File, newname string) error {
	// Unsupported kernels and filesystems must fail without an overwriting
	// rename fallback: the destination may have been created by another editor.
	return pickerRenameCapabilityError(unix.Renameat2(int(oldDirectory.Fd()), oldname, int(newDirectory.Fd()), newname, unix.RENAME_NOREPLACE))
}

// Renameat2 returns errno directly. ENOSYS means the syscall is unavailable.
// With the fixed RENAME_NOREPLACE flag, EINVAL and EOPNOTSUPP can indicate
// missing filesystem support. Preserve other errors without reclassifying them.
func pickerRenameCapabilityError(err error) error {
	if err == unix.ENOSYS || err == unix.EOPNOTSUPP || err == unix.EINVAL {
		return errors.Join(ErrPickerAtomicRenameUnsupported, err)
	}
	return err
}
