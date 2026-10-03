package autocomplete

import (
	"os"
	"path/filepath"
)

// renamePickerFileNoReplace moves an entry without overwriting another entry.
// Open both parents through Root before calling the native operation with only
// basenames, so nested paths cannot bypass Root's traversal restrictions. The
// final source entry is moved without following a symlink; its identity must be
// checked by the caller after capture.
func renamePickerFileNoReplace(root *os.Root, oldname, newname string) error {
	for _, name := range []string{oldname, newname} {
		if !filepath.IsLocal(name) || name == "." || filepath.Clean(name) != name {
			return os.ErrInvalid
		}
	}
	oldDirectory, err := openPickerRenameDirectory(root, filepath.Dir(oldname))
	if err != nil {
		return err
	}
	defer oldDirectory.Close()
	newDirectory, err := openPickerRenameDirectory(root, filepath.Dir(newname))
	if err != nil {
		return err
	}
	defer newDirectory.Close()
	return renamePickerEntriesNoReplace(oldDirectory, filepath.Base(oldname), newDirectory, filepath.Base(newname))
}

func openPickerRenameDirectory(root *os.Root, name string) (*os.File, error) {
	directory, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return directory.Open(".")
}
