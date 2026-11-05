//go:build !windows

package auth

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Go raises SIGPIPE for stdout writes. A private descriptor returns EPIPE instead,
// so the caller can report an already committed change without altering signal handling.
func writeProfileReceipt(writer io.Writer, write func(io.Writer) error) error {
	if file, ok := writer.(*os.File); !ok || file.Fd() != 1 {
		return write(writer)
	}
	fd, err := unix.FcntlInt(1, unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return err
	}
	receipt := os.NewFile(uintptr(fd), "profile receipt")
	return errors.Join(write(receipt), receipt.Close())
}
