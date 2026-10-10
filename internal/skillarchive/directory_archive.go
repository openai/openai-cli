// Package skillarchive prepares directory uploads as private temporary ZIP files.
package skillarchive

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Error identifies a failing relative path without printing absolute source or
// temporary paths. Unwrap preserves the original cause for errors.Is/As.
type Error struct {
	RelativePath string
	Reason       string
	err          error
}

func (e *Error) Error() string {
	if e.RelativePath == "" {
		return e.Reason
	}
	return fmt.Sprintf("%s: %q", e.Reason, e.RelativePath)
}

func (e *Error) Unwrap() error { return e.err }

func failure(path, reason string, err error) error {
	return &Error{RelativePath: filepath.ToSlash(path), Reason: reason, err: err}
}

// Archive owns a completed ZIP. Close releases its handle and removes its
// temporary file. Callers must close it after the upload, including on failure.
type Archive struct {
	file     *os.File
	path     string
	filename string
	size     int64
	ctx      context.Context
	once     sync.Once
	closeErr error
	staging  *temporaryArchive
}

func (a *Archive) Filename() string { return a.filename }
func (a *Archive) Size() int64      { return a.size }

func (a *Archive) Read(p []byte) (int, error) {
	if err := a.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := a.file.Read(p)
	if err != nil && err != io.EOF {
		err = failure("", "cannot read temporary skill archive", err)
	}
	return n, err
}

func (a *Archive) Seek(offset int64, whence int) (int64, error) {
	n, err := a.file.Seek(offset, whence)
	if err != nil {
		err = failure("", "cannot seek temporary skill archive", err)
	}
	return n, err
}

func (a *Archive) Close() error {
	a.once.Do(func() {
		a.closeErr = a.staging.close()
	})
	return a.closeErr
}

// Prepare packages every regular file, including dotfiles, beneath one folder
// named after directory. ZIP entries have lexical order and fixed timestamps.
// File modes are 0644 plus source executable bits; directory modes are 0755.
// Symlinks, special files, empty trees, and detected source changes are errors.
// Contents stream through a bounded buffer to disk; no API payload cap is added.
func Prepare(ctx context.Context, directory string) (_ *Archive, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, failure(".", "cannot resolve skill directory", err)
	}
	name := filepath.Base(absolute)
	if !validComponent(name) || filepath.Dir(absolute) == absolute {
		return nil, failure(".", "select a named skill directory", nil)
	}
	// The trailing separator makes a substituted FIFO fail as a non-directory.
	parentPath := filepath.Dir(absolute)
	if !strings.HasSuffix(parentPath, string(os.PathSeparator)) {
		parentPath += string(os.PathSeparator)
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, failure(".", "cannot open skill directory parent", err)
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, failure(".", "cannot inspect skill directory", err)
	}
	if !before.IsDir() {
		return nil, failure(".", "skill directory must be a directory, not a symlink or special file", nil)
	}
	anchor, err := parent.Open(".")
	if err != nil {
		return nil, failure(".", "cannot open skill directory parent", err)
	}
	defer anchor.Close()
	dir, err := openLeaf(anchor, name)
	if err != nil {
		return nil, failure(".", "cannot open skill directory", err)
	}
	defer dir.Close()
	if err := checkIdentity(dir, before, "."); err != nil {
		return nil, err
	}
	temporary, err := createTemporary()
	if err != nil {
		return nil, err
	}
	file := temporary.file
	archive := &Archive{file: file, path: temporary.path, filename: name + ".zip", ctx: ctx, staging: temporary}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, archive.Close())
		}
	}()
	writer := zip.NewWriter(contextWriter{ctx: ctx, writer: file})
	pack := packer{ctx: ctx, zip: writer, buffer: make([]byte, 32*1024), temporary: temporary}
	if err := pack.directory(dir, before, name, "."); err != nil {
		return nil, err
	}
	if pack.files == 0 {
		return nil, failure(".", "skill directory contains no regular files", nil)
	}
	if err := checkNamedIdentity(anchor, name, before, "."); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, failure("", "cannot finish temporary skill archive", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	archive.size, err = file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, failure("", "cannot measure temporary skill archive", err)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return archive, nil
}

func validComponent(name string) bool {
	return name != "." && name != ".." && name != "" && !strings.ContainsAny(name, "/\\")
}

type packer struct {
	ctx       context.Context
	zip       *zip.Writer
	buffer    []byte
	temporary *temporaryArchive
	files     uint64
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(p)
}

func (p *packer) header(name string, mode os.FileMode) (io.Writer, error) {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
	header.SetMode(mode)
	if mode.IsDir() {
		header.Method = zip.Store
	}
	writer, err := p.zip.CreateHeader(header)
	if err != nil {
		return nil, failure("", "cannot write temporary skill archive", err)
	}
	return writer, nil
}

func (p *packer) directory(dir *os.File, before os.FileInfo, prefix, relative string) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if _, err := p.header(prefix+"/", os.ModeDir|0755); err != nil {
		return err
	}
	var entries []os.DirEntry
	for {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		batch, err := dir.ReadDir(128)
		entries = append(entries, batch...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return failure(relative, "cannot read skill directory", err)
		}
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		if p.ctx.Err() != nil {
			return 0
		}
		return strings.Compare(entryName(a), entryName(b))
	})
	for _, entry := range entries {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(relative, entry.Name())
		if !validComponent(entry.Name()) {
			return failure(path, "skill paths cannot contain backslashes", nil)
		}
		info, err := entry.Info()
		if err != nil {
			return failure(path, "cannot inspect skill path", err)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return failure(path, "skill paths must be regular files or directories; symlinks and special files are unsupported", nil)
		}
		if err := p.entry(dir, entry.Name(), info, prefix+"/"+entry.Name(), path); err != nil {
			return err
		}
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	return checkIdentity(dir, before, relative)
}

func entryName(entry os.DirEntry) string {
	if entry.IsDir() {
		return entry.Name() + "/"
	}
	return entry.Name()
}

func (p *packer) entry(parent *os.File, name string, before os.FileInfo, archivePath, relative string) (resultErr error) {
	file, err := openLeaf(parent, name)
	if err != nil {
		return failure(relative, "cannot open skill path", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, failure(relative, "cannot close skill path", err))
		}
	}()
	if err := checkIdentity(file, before, relative); err != nil {
		return err
	}
	if before.IsDir() {
		if os.SameFile(before, p.temporary.directoryIdentity) {
			return failure(relative, "temporary directory must be outside the skill directory", nil)
		}
		if err := p.directory(file, before, archivePath, relative); err != nil {
			return err
		}
	} else {
		if os.SameFile(before, p.temporary.fileIdentity) {
			return failure(relative, "temporary directory must be outside the skill directory", nil)
		}
		writer, err := p.header(archivePath, 0644|before.Mode().Perm()&0111)
		if err != nil {
			return err
		}
		if err := p.copyFile(writer, file, before, relative); err != nil {
			return err
		}
		p.files++
	}
	return checkNamedIdentity(parent, name, before, relative)
}

func (p *packer) copyFile(writer io.Writer, file *os.File, before os.FileInfo, relative string) error {
	remaining := before.Size()
	for remaining > 0 {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		n, err := file.Read(p.buffer[:min(int64(len(p.buffer)), remaining)])
		if n > 0 {
			if written, err := writer.Write(p.buffer[:n]); err != nil {
				return failure("", "cannot write temporary skill archive", err)
			} else if written != n {
				return failure("", "cannot write temporary skill archive", io.ErrShortWrite)
			}
			remaining -= int64(n)
		}
		if err == io.EOF && remaining > 0 {
			return failure(relative, "skill file changed during packaging", err)
		}
		if err != nil && err != io.EOF {
			return failure(relative, "cannot read skill file", err)
		}
		if n == 0 && err == nil {
			return failure(relative, "cannot read skill file", io.ErrNoProgress)
		}
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	var extra [1]byte
	n, err := file.Read(extra[:])
	if n != 0 {
		return failure(relative, "skill file changed during packaging", nil)
	}
	if err != io.EOF {
		return failure(relative, "cannot read skill file", err)
	}
	return checkIdentity(file, before, relative)
}

func checkIdentity(file *os.File, before os.FileInfo, relative string) error {
	after, err := file.Stat()
	if err != nil {
		return failure(relative, "cannot inspect opened skill path", err)
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return failure(relative, "skill path changed during packaging", nil)
	}
	return nil
}

func checkNamedIdentity(parent *os.File, name string, before os.FileInfo, relative string) error {
	file, err := openLeaf(parent, name)
	if err != nil {
		return failure(relative, "skill path changed during packaging", err)
	}
	defer file.Close()
	return checkIdentity(file, before, relative)
}
