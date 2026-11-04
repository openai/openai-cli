package updatenotice

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

type updateInstallation struct {
	path                string
	root                *os.Root
	file                *os.File
	identity, directory os.FileInfo
}

func managedInstallation(current string, worker bool) (*updateInstallation, error) {
	if !automaticPlatform() || current != releaseVersion || !validCommit(releaseCommit) {
		return nil, errCache
	}
	v, valid := stableVersion(current)
	if !valid || v != current {
		return nil, errCache
	}
	path, err := managedExecutablePath(worker)
	if err != nil {
		return nil, err
	}
	installation, err := openInstallation(path)
	if err != nil {
		return nil, err
	}
	if !binaryIdentity(installation.file, current, releaseCommit) || !authorizedUpdateImage(installation, worker) {
		installation.close()
		return nil, errCache
	}
	return installation, nil
}

func openInstallation(path string) (*updateInstallation, error) {
	directory := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || !sameUpdateDirectory(resolved, directory) {
		return nil, errCache
	}
	// Both dedicated directories must belong to this user. Package-manager
	// directories, writable shared paths, and symlink launchers are unsupported.
	for _, dir := range []string{directory, filepath.Dir(directory)} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || !ownedUpdatePath(info) {
			return nil, errCache
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !ownedUpdatePath(info) {
		return nil, errCache
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	i := &updateInstallation{path: path, root: root, identity: info}
	i.directory, err = root.Stat(".")
	if err == nil {
		i.file, err = root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	}
	if err != nil || !i.unchanged() {
		i.close()
		return nil, errCache
	}
	return i, nil
}

func (i *updateInstallation) close() {
	if i.file != nil {
		_ = i.file.Close()
	}
	if i.root != nil {
		_ = i.root.Close()
	}
}

func (i *updateInstallation) unchanged() bool {
	if i.file == nil {
		return false
	}
	actual, err := i.file.Stat()
	if err != nil || !sameUpdateFile(i.identity, actual) || !safeOpenedUpdateFile(i.file) {
		return false
	}
	current, err := os.Lstat(i.path)
	if err != nil || !sameUpdateFile(i.identity, current) {
		return false
	}
	dir, err := os.Lstat(filepath.Dir(i.path))
	if err != nil || !dir.IsDir() || !ownedUpdatePath(dir) || !os.SameFile(i.directory, dir) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(i.path))
	return err == nil && sameUpdateDirectory(resolved, filepath.Dir(i.path))
}

func sameUpdateFile(a, b os.FileInfo) bool {
	return sameUpdateFileProperties(a, b) && a.ModTime() == b.ModTime()
}

func sameUpdateFileProperties(a, b os.FileInfo) bool {
	return b.Mode().IsRegular() && ownedUpdatePath(b) && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode()
}

// Writing an NTFS attachment stream can change the file's last-write time.
// Accept that change only for the same file with the authenticated bytes.
func preparedImageAfterTrust(ctx context.Context, next *os.File, before os.FileInfo, expectedHash string) (os.FileInfo, error) {
	after, err := next.Stat()
	if err != nil {
		return nil, err
	}
	if !sameUpdateFileProperties(before, after) || !safeOpenedUpdateFile(next) {
		return nil, errCache
	}
	// NTFS can defer timestamp updates while another handle remains open.
	// Recheck the bytes even when the last-write time still matches.
	hash, err := fileDigest(ctx, next)
	if err != nil {
		return nil, err
	}
	if hash != expectedHash {
		return nil, errPreparedUpdate
	}
	return after, nil
}

func (i *updateInstallation) digest(ctx context.Context) (string, error) {
	if !i.unchanged() {
		return "", errCache
	}
	digest, err := fileDigest(ctx, i.file)
	if !i.unchanged() {
		return "", errCache
	}
	return digest, err
}

func openUpdateFile(root *os.Root, name string) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil || !privateFile(info) || !ownedUpdatePath(info) {
		return nil, errCache
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil || !sameUpdateFile(info, actual) || !safeOpenedUpdateFile(file) {
		file.Close()
		return nil, errCache
	}
	return file, nil
}

func fileDigest(ctx context.Context, file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBinaryBytes {
		return "", errCache
	}
	hash := sha256.New()
	_, err = io.Copy(hash, contextReader{ctx, io.NewSectionReader(file, 0, info.Size())})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

const stampPrefix = "github.com/openai/openai-cli/internal/updatenotice."

func binaryIdentity(file *os.File, version, commit string) bool {
	v, c, ok := binaryReleaseStamp(file)
	return ok && v == version && c == commit
}

// Inspect the opened executable without running it. A stamp identifies a
// release only after the caller authenticates the bytes or matches a receipt.
func binaryReleaseStamp(file *os.File) (string, string, bool) {
	info, err := buildinfo.Read(file)
	if err != nil || info.Path != "command-line-arguments" {
		return "", "", false
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return "", "", false
	}
	values := map[string]string{}
	fields := strings.Fields(settings["-ldflags"])
	for index, field := range fields {
		if field != "-X" || index+1 == len(fields) {
			continue
		}
		key, value, ok := strings.Cut(fields[index+1], "=")
		if ok && strings.HasPrefix(key, stampPrefix) {
			if _, exists := values[key]; exists {
				return "", "", false
			}
			values[key] = value
		}
	}
	return values[stampPrefix+"releaseVersion"], values[stampPrefix+"releaseCommit"], true
}

func validDigest(s string) bool { return validHex(s, 64) }
func validCommit(s string) bool { return validHex(s, 40) }
func validHex(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
