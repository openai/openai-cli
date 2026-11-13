// Package skills prepares local skill folders for the existing ZIP upload transport.
package skills

import (
	"archive/zip"
	"cmp"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/request"
)

func folderError(message string, cause error) error {
	return &commands.LocalError{Message: "Skill folder (--files): " + message, Cause: cause}
}

// ArchiveFolder snapshots regular files before HTTP starts. Its private ZIP handle
// transfers to the shared uploader, including on cancellation and transport errors.
func ArchiveFolder(ctx context.Context, path string, selected os.FileInfo) (request.FileUpload, error) {
	var none request.FileUpload
	lookup := folderLookupPath(path)
	named, err := os.Lstat(lookup)
	if err != nil {
		return none, changed(err)
	}
	if named.Mode()&os.ModeSymlink != 0 {
		return none, folderError("Select the actual folder instead of a symbolic link.", nil)
	}
	if !named.IsDir() || !os.SameFile(named, selected) {
		return none, changed(nil)
	}
	namePath, err := folderNamePath(lookup)
	if err != nil {
		return none, folderError("Cannot resolve the folder.", err)
	}
	if namePath != lookup {
		// Verify that the path used for the archive name identifies the selected folder.
		resolved, err := os.Stat(namePath)
		if err != nil || !os.SameFile(resolved, selected) {
			return none, changed(err)
		}
	}
	name := filepath.Base(namePath)
	if name == "." || name == ".." || name == string(os.PathSeparator) || !safeName(name) || hasDrivePrefix(name) {
		return none, folderError("Select a named skill folder.", nil)
	}
	// The final directory component prevents a replacement FIFO from blocking OpenRoot.
	root, err := os.OpenRoot(path + string(os.PathSeparator) + ".")
	if err != nil {
		return none, folderError("Cannot open the folder. Check its permissions.", err)
	}
	defer root.Close()
	tmp, err := archiveTemp()
	if err != nil {
		return none, folderError("Cannot create the temporary ZIP.", err)
	}
	if err := pack(ctx, root, selected, name, tmp); err != nil {
		return none, errors.Join(err, tmp.Close())
	}
	named, err = os.Lstat(lookup)
	if err != nil || !os.SameFile(selected, named) {
		return none, errors.Join(changed(err), tmp.Close())
	}
	upload, err := request.FileUploadFromFile(tmp)
	if err != nil {
		return none, folderError("Cannot read the temporary ZIP.", err)
	}
	upload.FileName = name + ".zip"
	upload.MediaType = "application/zip"
	return upload, nil
}

// A trailing separator or "." makes Lstat follow a directory symlink. Strip
// only these suffixes so the kernel can resolve ".." after symlink ancestors.
func folderLookupPath(path string) string {
	root := len(filepath.VolumeName(path))
	if root < len(path) && os.IsPathSeparator(path[root]) {
		root++
	}
	for {
		for len(path) > root && os.IsPathSeparator(path[len(path)-1]) {
			path = path[:len(path)-1]
		}
		dir, base := filepath.Split(path)
		if base != "." || len(dir) <= root {
			return path
		}
		path = dir
	}
}

// Use the selected directory's name when the path does not end in a name.
func folderNamePath(path string) (string, error) {
	switch filepath.Base(path) {
	case ".":
		return filepath.Abs(path)
	case "..":
		if runtime.GOOS == "windows" {
			return filepath.Abs(path)
		}
		if !filepath.IsAbs(path) {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			path = cwd + string(os.PathSeparator) + path
		}
		return filepath.EvalSymlinks(path)
	default:
		return path, nil
	}
}

// A drive-qualified top-level ZIP name can extract as an absolute Windows path.
func hasDrivePrefix(name string) bool {
	return len(name) >= 2 && name[1] == ':' && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z'))
}

func safeName(name string) bool {
	return utf8.ValidString(name) && !strings.ContainsRune(name, '\\') && strings.IndexFunc(name, unicode.IsControl) < 0
}

type folderArchive struct {
	ctx      context.Context
	root     *os.Root
	zip      *zip.Writer
	tempDir  os.FileInfo
	tempName string
	files    int
	buf      []byte
}

func pack(ctx context.Context, root *os.Root, selected os.FileInfo, name string, tmp *os.File) error {
	p := folderArchive{ctx: ctx, root: root, zip: zip.NewWriter(tmp), buf: make([]byte, 32*1024)}
	if runtime.GOOS == "windows" {
		// Windows keeps the file named but may deny other opens while deletion is pending.
		// Identify its pinned parent before walking, so listing TMP inside a skill works.
		var err error
		p.tempDir, err = os.Stat(os.TempDir())
		if err != nil {
			return folderError("Cannot inspect the temporary directory.", err)
		}
		p.tempName = filepath.Base(tmp.Name())
	}
	// Literal Huffman coding reduces text size without producing extremely high
	// compression ratios for repetitive assets. The API inspects archive ratios.
	var compressor *flate.Writer
	p.zip.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		if compressor == nil {
			var err error
			compressor, err = flate.NewWriter(w, flate.HuffmanOnly)
			return compressor, err
		}
		compressor.Reset(w)
		return compressor, nil
	})
	before, err := root.Stat(".")
	if err != nil || !os.SameFile(before, selected) {
		return changed(err)
	}
	if err := p.add(".", name, before); err != nil {
		return err
	}
	if p.files == 0 {
		return folderError("Add files to the empty folder.", nil)
	}
	if err := p.zip.Close(); err != nil {
		return folderError("Cannot finish the temporary ZIP. Check available disk space.", err)
	}
	return nil
}

func changed(err error) error {
	return folderError("The folder changed during packaging. Retry with a stable copy.", err)
}

// Identify the entry inside the selected bundle without exposing the user's local path.
func memberError(member, message string, cause error) error {
	if member == "." {
		return folderError(message, cause)
	}
	return folderError(fmt.Sprintf("Entry %q: %s", filepath.ToSlash(member), message), cause)
}

func same(a, b os.FileInfo) bool {
	return b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (p *folderArchive) add(path, name string, info os.FileInfo) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return memberError(path, "Remove symbolic links and special files before uploading.", nil)
	}
	// Nonblocking open prevents a replacement FIFO from waiting for a writer.
	f, err := p.root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return memberError(path, "Cannot open this entry. Check its permissions.", err)
	}
	opened, err := f.Stat()
	if err != nil || !same(info, opened) {
		return errors.Join(changed(err), f.Close())
	}
	if info.IsDir() {
		entries, err := f.ReadDir(-1)
		if err = errors.Join(err, f.Close()); err != nil {
			return memberError(path, "Cannot list this directory. Check its permissions.", err)
		}
		if os.SameFile(info, p.tempDir) {
			entries = slices.DeleteFunc(entries, func(entry os.DirEntry) bool { return entry.Name() == p.tempName })
		}
		if len(entries) == 0 && path != "." {
			header := archiveHeader(name+"/", os.ModeDir|0755)
			if _, err := p.zip.CreateHeader(&header); err != nil {
				return folderError("Cannot create a ZIP directory entry.", err)
			}
		}
		slices.SortFunc(entries, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })
		for _, entry := range entries {
			if !safeName(entry.Name()) {
				return memberError(filepath.Join(path, entry.Name()), "Rename the entry to use a portable ZIP name.", nil)
			}
			child := filepath.Join(path, entry.Name())
			stat, err := p.root.Lstat(child)
			if err != nil {
				return memberError(child, "Cannot inspect this entry. Check its path and permissions.", err)
			}
			if err := p.add(child, name+"/"+entry.Name(), stat); err != nil {
				return err
			}
		}
	} else {
		defer f.Close()
		header := archiveHeader(name, 0644|info.Mode().Perm()&0111)
		w, err := p.zip.CreateHeader(&header)
		if err != nil {
			return folderError("Cannot create the ZIP entry.", err)
		}
		n, err := io.CopyBuffer(w, io.LimitReader(contextReader{p.ctx, f}, info.Size()), p.buf)
		if err != nil {
			return memberError(path, "Cannot package this file. Check read permissions and temporary disk space.", err)
		}
		// A virtual file can report a size that differs from its readable bytes.
		extra, err := f.Read(p.buf[:1])
		if n != info.Size() || extra != 0 || err != io.EOF {
			return changed(err)
		}
		p.files++
	}
	after, err := p.root.Lstat(path)
	if err != nil || !same(info, after) {
		return changed(err)
	}
	return nil
}

func archiveHeader(name string, mode os.FileMode) zip.FileHeader {
	h := zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
	h.SetMode(mode)
	return h
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}
