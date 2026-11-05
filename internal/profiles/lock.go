package profiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

var (
	errDirectoryUnsafe = errors.New("profiles require a private configuration directory owned by the current user")
	errLockUnsafe      = errors.New("profile lock must be a private regular file owned by the current user")
)

func sameSnapshot(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}

func privateFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && privatePermissions(info)
}

// Keep the sibling lock permanently. Removing it can split waiting writers
// between different inodes. Closing its descriptor releases the kernel lock.
func lockFile(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	name = "." + name + ".lock"
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|openFlags(), 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := root.Lstat(name)
		if statErr != nil {
			return nil, statErr
		}
		if !privateFile(info) {
			return nil, errLockUnsafe
		}
		file, err = root.OpenFile(name, os.O_RDWR|openFlags(), 0)
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
		if !privateFile(identity) || !privateFile(current) || !os.SameFile(identity, current) {
			return ErrChanged
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
		locked, err := tryLock(file)
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

func openDirectory(path string, create bool) (*os.Root, string, error) {
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
		return nil, "", errDirectoryUnsafe
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		if err == nil {
			err = ErrChanged
		}
		return nil, "", err
	}
	// Existing CLI configuration directories can predate profiles.
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
	if !privatePermissions(info) || !privatePermissions(actual) {
		root.Close()
		return nil, "", errDirectoryUnsafe
	}
	return root, name, nil
}
