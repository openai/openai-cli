//go:build darwin

package autocomplete

import (
	"os"

	"golang.org/x/sys/unix"
)

func renamePickerEntriesNoReplace(oldDirectory *os.File, oldname string, newDirectory *os.File, newname string) error {
	return unix.RenameatxNp(int(oldDirectory.Fd()), oldname, int(newDirectory.Fd()), newname, unix.RENAME_EXCL)
}
