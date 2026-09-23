//go:build !windows

package binaryparam

import (
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// CancellableFile takes ownership of file on success. Regular files retain their
// usual reader; owned FIFOs use nonblocking reads so Close can interrupt them on
// systems where Go deliberately excludes FIFOs from its runtime poller.
func CancellableFile(file *os.File, info os.FileInfo) (io.ReadCloser, error) {
	if info.Mode()&os.ModeNamedPipe == 0 {
		return file, nil
	}
	fd := int(file.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, &os.PathError{Op: "setnonblock", Path: file.Name(), Err: err}
	}
	return &fifoReader{file: file, fd: fd}, nil
}

type fifoReader struct {
	file     *os.File
	fd       int
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func (r *fifoReader) Name() string { return r.file.Name() }

func (r *fifoReader) Read(p []byte) (int, error) {
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return 0, &os.PathError{Op: "read", Path: r.Name(), Err: os.ErrClosed}
		}
		if len(p) == 0 {
			r.mu.Unlock()
			return 0, nil
		}
		n, err := unix.Read(r.fd, p)
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			// Hold ownership while polling so Close cannot recycle the descriptor
			// underneath poll/read. A bounded wait also detects EOF if a platform
			// misses the last-writer event, without limiting upload size or duration.
			fds := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
			_, err = unix.Poll(fds, 50)
			r.mu.Unlock()
			if err != nil && err != unix.EINTR {
				return 0, &os.PathError{Op: "poll", Path: r.Name(), Err: err}
			}
			continue
		}
		r.mu.Unlock()
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, &os.PathError{Op: "read", Path: r.Name(), Err: err}
		}
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
}

func (r *fifoReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.closeErr = r.file.Close()
	}
	return r.closeErr
}
