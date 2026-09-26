package imagegallery

import (
	"errors"
	"fmt"
	"os"
)

// The file is deliberately never unlinked: removing a lock path allows another
// process to lock a different inode while an existing holder still runs.
func acquireLock(path string) (*os.File, error) {
	return openLock(path, os.O_CREATE|os.O_RDWR)
}

// Inspection opens the existing stable lock without creating or writing it.
func openLock(path string, flags int) (*os.File, error) {
	if err := checkPrivate(path, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) { _ = file.Close(); return nil, err }
	if err = checkPrivate(path, false); err != nil {
		return fail(err)
	}
	descriptor, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(descriptor, current) {
		return fail(errors.New("image gallery lock changed while opening"))
	}
	if err = lockFile(file); err != nil {
		return fail(err)
	}
	return file, nil
}

func lockError(err error) error { return fmt.Errorf("lock image gallery: %w", err) }
