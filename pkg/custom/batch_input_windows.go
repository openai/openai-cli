//go:build windows

package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"
)

func readBatchRequestInput(ctx context.Context, file *os.File, data []byte) (int, error) {
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
	// Process stdin uses a synchronous Windows handle. Peek before reading so
	// cancellation never needs to close that borrowed handle or change its mode.
	// Control avoids Fd(), which can disassociate an overlapped handle from IOCP.
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var available uint32
		var peekErr error
		err := connection.Control(func(handle uintptr) {
			ok, _, cause := procPeekNamedPipe.Call(handle, 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
			if ok == 0 {
				peekErr = cause
			}
		})
		if err != nil {
			if _, fileErr := file.Stat(); fileErr != nil {
				return 0, fileErr
			}
			return 0, err
		}
		if errors.Is(peekErr, syscall.ERROR_BROKEN_PIPE) {
			return 0, io.EOF
		}
		if peekErr != nil {
			return 0, peekErr
		}
		if available > 0 {
			if uint64(available) < uint64(len(data)) {
				data = data[:int(available)]
			}
			return file.Read(data)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}
