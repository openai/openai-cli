//go:build linux

package autocomplete

import (
	"os"

	"golang.org/x/sys/unix"
)

func renamePickerEntriesNoReplace(oldDirectory *os.File, oldname string, newDirectory *os.File, newname string) error {
	// Unsupported kernels and filesystems must fail without an overwriting
	// rename fallback: the destination may have been created by another editor.
	return unix.Renameat2(int(oldDirectory.Fd()), oldname, int(newDirectory.Fd()), newname, unix.RENAME_NOREPLACE)
}
