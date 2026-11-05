package updatenotice

import (
	"bytes"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const quarantineAttribute = "com.apple.quarantine"

// Keep the incoming package's own quarantine. When the replacement has none,
// carry forward the installed image's exact native attribute. Both descriptors
// identify checked executable bytes. Do not transfer any other metadata.
func preservePreparedImageTrust(current, next *os.File) error {
	if current == nil || next == nil {
		return errCache
	}
	incoming, err := readQuarantine(next)
	if err != nil || incoming != nil {
		return err
	}
	existing, err := readQuarantine(current)
	if err != nil || existing == nil {
		return err
	}
	if err := unix.Fsetxattr(int(next.Fd()), quarantineAttribute, existing, unix.XATTR_CREATE); err != nil {
		return err
	}
	if !preservedQuarantine(current, next) {
		return errCache
	}
	return next.Sync()
}

// A copy can succeed on a filesystem that discards quarantine.
func preservedQuarantine(source, target *os.File) bool {
	existing, err := readQuarantine(source)
	if err != nil {
		return false
	}
	if existing == nil {
		return true
	}
	incoming, err := readQuarantine(target)
	return err == nil && incoming != nil && bytes.Equal(existing, incoming)
}

func readQuarantine(file *os.File) ([]byte, error) {
	size, err := unix.Fgetxattr(int(file.Fd()), quarantineAttribute, nil)
	if errors.Is(err, unix.ENOATTR) || errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if size == 0 || size > 64<<10 {
		return nil, errCache
	}
	data := make([]byte, size)
	n, err := unix.Fgetxattr(int(file.Fd()), quarantineAttribute, data)
	if err != nil || n != size {
		return nil, errCache
	}
	return data, nil
}
