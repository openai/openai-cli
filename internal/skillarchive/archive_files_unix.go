//go:build unix

package skillarchive

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Only one validated leaf is opened relative to an already opened directory.
// NONBLOCK avoids waiting on a FIFO substituted after directory inspection.
func openLeaf(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name)), nil
}

func createPrivateDirectory(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	parent, err := root.Open(".")
	if err != nil {
		return nil, nil, err
	}
	defer parent.Close()
	if err := root.Mkdir(name, 0700); err != nil {
		return nil, nil, err
	}
	// Retain identity before another descriptor allocation can fail. Cleanup can
	// then remove the verified empty directory even under descriptor exhaustion.
	identity, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !identity.IsDir() {
		return nil, nil, os.ErrInvalid
	}
	directory, err := openLeaf(parent, name)
	return directory, identity, err
}

func createPrivateArchiveFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
}
