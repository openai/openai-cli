package skillarchive

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const temporaryArchiveName = "archive.zip"

// Keep both roots open until cleanup. Renaming either directory pathname must
// not redirect removal into a replacement directory. Private permissions restrict
// access, but do not isolate hostile processes using the same account.
type temporaryArchive struct {
	file              *os.File
	root              *os.Root
	parent            *os.Root
	name              string
	path              string
	fileIdentity      os.FileInfo
	directoryIdentity os.FileInfo
}

func createTemporary() (_ *temporaryArchive, resultErr error) {
	parentPath := os.TempDir()
	if !strings.HasSuffix(parentPath, string(os.PathSeparator)) {
		parentPath += string(os.PathSeparator)
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, failure("", "cannot open temporary directory", err)
	}
	temporary := &temporaryArchive{parent: parent, name: "openai-skill-" + rand.Text()}
	temporary.path = filepath.Join(parent.Name(), temporary.name, temporaryArchiveName)
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, temporary.close())
		}
	}()
	directory, identity, err := createPrivateDirectory(parent, temporary.name)
	if directory != nil {
		defer directory.Close()
	}
	if identity != nil && identity.IsDir() {
		temporary.directoryIdentity = identity
	}
	if err != nil {
		return nil, failure("", "cannot create private temporary skill directory", err)
	}
	created, err := directory.Stat()
	if err != nil {
		return nil, failure("", "cannot inspect private temporary skill directory", err)
	}
	if !created.IsDir() || !os.SameFile(identity, created) {
		return nil, failure("", "temporary skill directory changed during creation", nil)
	}
	// A trailing separator prevents blocking on a FIFO substituted before open.
	temporary.root, err = parent.OpenRoot(temporary.name + string(os.PathSeparator))
	if err != nil {
		return nil, failure("", "cannot open private temporary skill directory", err)
	}
	opened, err := temporary.root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(temporary.directoryIdentity, opened) {
		return nil, failure("", "temporary skill directory changed during creation", err)
	}
	temporary.file, err = createPrivateArchiveFile(temporary.root, temporaryArchiveName)
	if err != nil {
		return nil, failure("", "cannot create temporary skill archive", err)
	}
	temporary.fileIdentity, err = temporary.file.Stat()
	if err != nil {
		return nil, failure("", "cannot inspect temporary skill archive", err)
	}
	return temporary, nil
}

func (t *temporaryArchive) close() (resultErr error) {
	if t.file != nil {
		if err := t.file.Close(); err != nil {
			resultErr = failure("", "cannot close temporary skill archive", err)
		}
		resultErr = errors.Join(resultErr, removeOwnedTemporary(t.root, temporaryArchiveName, t.fileIdentity, "archive"))
	}
	if t.root != nil {
		if err := t.root.Close(); err != nil {
			resultErr = errors.Join(resultErr, failure("", "cannot close temporary skill directory", err))
		}
	}
	if t.directoryIdentity != nil {
		resultErr = errors.Join(resultErr, removeOwnedTemporary(t.parent, t.name, t.directoryIdentity, "directory"))
	}
	if err := t.parent.Close(); err != nil {
		resultErr = errors.Join(resultErr, failure("", "cannot close temporary directory parent", err))
	}
	return resultErr
}

func removeOwnedTemporary(root *os.Root, name string, identity os.FileInfo, kind string) error {
	if identity == nil {
		return failure("", "cannot verify temporary skill "+kind+" for cleanup", nil)
	}
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(identity, current) {
		return failure("", "temporary skill "+kind+" changed before cleanup", err)
	}
	// Remove only this verified entry. Never sweep unexpected directory contents.
	// There is no portable atomic unlink-by-inode. A hostile same-UID process can
	// still replace this entry between Lstat and Remove, despite private permissions.
	if err := root.Remove(name); err != nil {
		return failure("", "cannot remove temporary skill "+kind, err)
	}
	return nil
}
