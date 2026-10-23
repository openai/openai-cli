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

func managedInstallation(current string) (*updateInstallation, error) {
	if !automaticPlatform() || current != releaseVersion || !validCommit(releaseCommit) {
		return nil, errCache
	}
	v, valid := stableVersion(current)
	if !valid || v != current {
		return nil, errCache
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".openai", "bin", "openai")
	executable, err := os.Executable()
	if err != nil || filepath.Clean(executable) != path {
		return nil, errCache
	}
	installation, err := openInstallation(path)
	if err != nil {
		return nil, err
	}
	if !binaryIdentity(installation.file, current, releaseCommit) {
		installation.close()
		return nil, errCache
	}
	return installation, nil
}

func openInstallation(path string) (*updateInstallation, error) {
	directory := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
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
		i.file, err = root.OpenFile("openai", os.O_RDONLY|syscall.O_NONBLOCK, 0)
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
	if err != nil || !sameUpdateFile(i.identity, actual) {
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
	return err == nil && resolved == filepath.Dir(i.path)
}

func sameUpdateFile(a, b os.FileInfo) bool {
	return b.Mode().IsRegular() && ownedUpdatePath(b) && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime() == b.ModTime() && a.Mode() == b.Mode()
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
	if err != nil || !sameUpdateFile(info, actual) {
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
	info, err := buildinfo.Read(file)
	if err != nil || info.Path != "command-line-arguments" {
		return false
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return false
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
				return false
			}
			values[key] = value
		}
	}
	return values[stampPrefix+"releaseVersion"] == version && values[stampPrefix+"releaseCommit"] == commit
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
