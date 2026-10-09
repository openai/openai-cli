//go:build !windows

package custom

import (
	"context"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// readTokenizerFile preserves the caller's descriptor flags and deadlines.
// Pipe input needs an exclusive consumer: another descriptor or process could
// otherwise drain a blocking pipe between its readiness event and read.
func readTokenizerFile(ctx context.Context, file *os.File, data []byte) (int, error) {
	if ctx.Done() == nil || len(data) == 0 {
		return file.Read(data)
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return file.Read(data)
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return 0, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, readErr, ready := 0, error(nil), false
		err := connection.Read(func(fd uintptr) bool {
			events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			_, readErr = unix.Poll(events, 25)
			if readErr == nil {
				readErr = ctx.Err()
			}
			if readErr == nil && events[0].Revents != 0 {
				ready = true
				n, readErr = unix.Read(int(fd), data)
				n = max(0, n)
			}
			// Return after one attempt. RawConn checks caller deadlines and closure
			// again before another callback; it must not enter its unbounded wait.
			return true
		})
		if canceled := ctx.Err(); canceled != nil {
			return n, canceled
		}
		if err != nil {
			// RawConn exposes an internal close error. Stat retains os.File's
			// public ErrClosed identity when the caller closes its input.
			if _, fileErr := file.Stat(); fileErr != nil {
				return 0, fileErr
			}
			return 0, err
		}
		if readErr == unix.EINTR || readErr == unix.EAGAIN || readErr == unix.EWOULDBLOCK {
			continue
		}
		if readErr != nil {
			return 0, readErr
		}
		if !ready {
			continue
		}
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
}
