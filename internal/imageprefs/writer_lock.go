package imageprefs

import (
	"context"
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"
)

// Keep the lock file permanently: unlinking it could give waiting writers a
// different inode to lock. Close releases the kernel lock, including on exit.
func lockPreferences(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	name = "." + name + ".lock"
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NONBLOCK, 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := root.Lstat(name)
		if statErr != nil {
			return nil, statErr
		}
		if !privateRegularLock(info) {
			return nil, errors.New("image preference lock must be a private regular file")
		}
		file, err = root.OpenFile(name, os.O_RDWR|syscall.O_NONBLOCK, 0)
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
		if !privateRegularLock(identity) || !privateRegularLock(current) || !os.SameFile(identity, current) {
			return errors.New("image preference lock changed while opening or waiting")
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
		locked, err := tryWriterLock(file)
		if err != nil {
			return nil, err
		}
		if locked {
			// A waiter must not write while holding a removed or replaced lock.
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

func privateRegularLock(info os.FileInfo) bool {
	return info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0)
}
