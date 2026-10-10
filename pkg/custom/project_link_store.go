package custom

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// This limit applies only to local project links, never API request or response data.
const maxProjectLinkBytes = 1 << 20

var (
	errProjectLinksChanged         = errors.New("project links changed while reading; retry the command")
	errProjectLinksTooLarge        = errors.New("project links exceed the 1 MiB local registry limit; existing links were kept")
	errProjectLinksInvalid         = errors.New("invalid project links; existing links were kept")
	errProjectLinkInputInvalid     = errors.New("invalid folder or project identifier; existing links were kept")
	errProjectLinkFileUnsafe       = errors.New("project links must be a private regular file owned by the current user")
	errProjectLinkDirectoryUnsafe  = errors.New("project links require a private configuration directory owned by the current user")
	errProjectLinkLockUnsafe       = errors.New("project link lock must be a private regular file owned by the current user")
	errProjectLinkLockChanged      = errors.New("project link lock changed while opening or waiting")
	errProjectLinkDirectoryChanged = errors.New("configuration directory changed while opening")
)

func loadProjectLinks(ctx context.Context, path string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// An absent registry cannot select a project. Preserve existing configuration
	// layouts without opening or consuming a registry installed after this check.
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	root, name, err := openProjectLinkDirectory(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readProjectLinks(ctx, root, name)
}

func readProjectLinks(ctx context.Context, root *os.Root, name string) (map[string]string, error) {
	for range 64 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		if err != nil {
			return nil, err
		}
		links, err := readProjectLinkSnapshot(ctx, root, name, info)
		if !errors.Is(err, errProjectLinksChanged) {
			return links, err
		}
	}
	return nil, errProjectLinksChanged
}

func readProjectLinkSnapshot(ctx context.Context, root *os.Root, name string, expected os.FileInfo) (map[string]string, error) {
	if !privateProjectLinkFile(expected) {
		return nil, errProjectLinkFileUnsafe
	}
	file, err := root.OpenFile(name, os.O_RDONLY|projectLinkOpenFlags(), 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !privateProjectLinkFile(actual) || !sameProjectLinkSnapshot(expected, actual) {
		return nil, errProjectLinksChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProjectLinkBytes+1))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errProjectLinksChanged
		}
		return nil, err
	}
	if !privateProjectLinkFile(current) || !sameProjectLinkSnapshot(actual, current) {
		return nil, errProjectLinksChanged
	}
	if len(data) > maxProjectLinkBytes {
		return nil, errProjectLinksTooLarge
	}
	return decodeProjectLinks(data)
}

func sameProjectLinkSnapshot(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}

func privateProjectLinkFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && privateProjectLinkPermissions(info)
}

func decodeProjectLinks(data []byte) (map[string]string, error) {
	invalid := errProjectLinksInvalid
	if !utf8.Valid(data) {
		return nil, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	links := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		directory, ok := token.(string)
		if err != nil || !ok || !validProjectLinkDirectory(directory) {
			return nil, invalid
		}
		if _, exists := links[directory]; exists {
			return nil, invalid
		}
		var project string
		if err := decoder.Decode(&project); err != nil || !validLinkedProject(project) {
			return nil, invalid
		}
		links[directory] = project
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || decoder.Decode(new(any)) != io.EOF {
		return nil, invalid
	}
	return links, nil
}

func validProjectLinkDirectory(directory string) bool {
	return filepath.IsAbs(directory) && filepath.Clean(directory) == directory && utf8.ValidString(directory) && !strings.ContainsRune(directory, 0)
}

// Keep the sibling lock permanently. Removing it can split waiting writers
// between different inodes. Closing its descriptor releases the kernel lock.
func lockProjectLinks(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	name = "." + name + ".lock"
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|projectLinkOpenFlags(), 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := root.Lstat(name)
		if statErr != nil {
			return nil, statErr
		}
		if !privateProjectLinkFile(info) {
			return nil, errProjectLinkLockUnsafe
		}
		file, err = root.OpenFile(name, os.O_RDWR|projectLinkOpenFlags(), 0)
	}
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			file.Close()
		}
	}()
	identity, err := file.Stat()
	if err != nil {
		return nil, err
	}
	checkIdentity := func() error {
		current, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !privateProjectLinkFile(identity) || !privateProjectLinkFile(current) || !os.SameFile(identity, current) {
			return errProjectLinkLockChanged
		}
		return nil
	}
	if err := checkIdentity(); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		locked, err := tryProjectLinkLock(file)
		if err != nil {
			return nil, err
		}
		if locked {
			if err := checkIdentity(); err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ok = true
			return file, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Cooperating writers hold one lock across validation and atomic replacement.
// Advisory locks cannot coordinate programs that ignore the lock protocol.
func updateProjectLink(ctx context.Context, path, directory, project string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validProjectLinkDirectory(directory) || (project != "" && !validLinkedProject(project)) {
		return nil, errProjectLinkInputInvalid
	}
	root, name, err := openProjectLinkDirectory(path, true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	lock, err := lockProjectLinks(ctx, root, name)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	links, err := readProjectLinks(ctx, root, name)
	if err != nil {
		return nil, err
	}
	if project == "" {
		delete(links, directory)
	} else {
		links[directory] = project
	}
	data, err := json.MarshalIndent(links, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > maxProjectLinkBytes {
		return nil, errProjectLinksTooLarge
	}
	temporary := ".project-links-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return nil, err
	}
	// Rename replaces the directory entry, never a substituted symlink's target.
	if err := root.Rename(temporary, name); err != nil {
		return nil, err
	}
	return links, nil
}

func openProjectLinkDirectory(path string, create bool) (*os.Root, string, error) {
	directory, name := filepath.Dir(path), filepath.Base(path)
	if create {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, "", err
		}
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", errProjectLinkDirectoryUnsafe
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		if err == nil {
			err = errProjectLinkDirectoryChanged
		}
		return nil, "", err
	}
	// Existing CLI configuration directories can predate project linking.
	// An absent registry must not introduce new permission requirements.
	if !create {
		if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
			root.Close()
			return nil, "", os.ErrNotExist
		} else if err != nil {
			root.Close()
			return nil, "", err
		}
	}
	if !privateProjectLinkPermissions(info) || !privateProjectLinkPermissions(actual) {
		root.Close()
		return nil, "", errProjectLinkDirectoryUnsafe
	}
	return root, name, nil
}
