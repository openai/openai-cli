package terminalimage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Reopen exactly stdout's tty, not /dev/tty or a dup sharing stdout's flags.
// This descriptor alone is nonblocking. Never change termios or keyboard input.
func openKittyTTY(out *os.File) (int, error) {
	fd := int(out.Fd())
	var original unix.Stat_t
	if err := unix.Fstat(fd, &original); err != nil {
		return -1, err
	}
	if original.Mode&unix.S_IFMT != unix.S_IFCHR {
		return -1, errors.New("Kitty output requires a terminal")
	}
	if _, err := unix.IoctlGetTermios(fd, unix.TIOCGETA); err != nil {
		return -1, err
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE == unix.O_RDONLY {
		return -1, errors.New("Kitty output requires a writable terminal")
	}
	var path [unix.PathMax]byte
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), unix.F_GETPATH, uintptr(unsafe.Pointer(&path[0])))
	if errno != 0 {
		return -1, errno
	}
	end := bytes.IndexByte(path[:], 0)
	if end <= 0 {
		return -1, errors.New("Kitty terminal path is unavailable")
	}
	owned, err := unix.Open(string(path[:end]), unix.O_WRONLY|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	var reopened unix.Stat_t
	if err := unix.Fstat(owned, &reopened); err != nil || original.Dev != reopened.Dev || original.Ino != reopened.Ino || original.Rdev != reopened.Rdev || reopened.Mode&unix.S_IFMT != unix.S_IFCHR {
		unix.Close(owned)
		return -1, errors.New("Kitty terminal identity changed")
	}
	return owned, nil
}

func copyKittyFrames(out *os.File, input io.Reader) error {
	fd, err := openKittyTTY(out)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return readKittyFrames(input, func(frame []byte) error {
		return writeKittyTTYFrame(frame, func() error {
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
			for {
				_, err := unix.Poll(poll, -1)
				if err == unix.EINTR {
					continue
				}
				if err != nil {
					return err
				}
				if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
					return errors.New("Kitty terminal output closed")
				}
				if poll[0].Revents&unix.POLLOUT != 0 {
					return nil
				}
			}
		}, func(data []byte) (int, error) { return unix.Write(fd, data) })
	})
}

// Poll before EVERY frame, including after a successful write. A blocking write
// or a resumed tail can leak payload after VINTR flushes the graphics header.
func writeKittyTTYFrame(frame []byte, ready func() error, write func([]byte) (int, error)) error {
	for {
		if err := ready(); err != nil {
			return err
		}
		n, err := write(frame)
		if n > 0 {
			if n != len(frame) {
				return errors.Join(io.ErrShortWrite, err)
			}
			return err
		}
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err == nil {
			err = io.ErrShortWrite
		}
		return err
	}
}
