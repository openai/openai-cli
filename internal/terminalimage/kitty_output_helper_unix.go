//go:build darwin || linux

package terminalimage

import (
	"errors"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

const kittyOutputHelperArgument = "__image-output"

func kittyReadPipe(fd int) error {
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFIFO {
		return errors.New("image output helper requires pipe descriptors")
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		return errors.New("image output helper requires read-only pipes")
	}
	return nil
}

// RunKittyOutputHelper handles only the private resident writer invocation.
// The process itself owns output. Parent death exits it even during a blocked
// write, and the parent can always kill and reap its direct child on failure.
func RunKittyOutputHelper(args []string) (bool, error) {
	if len(args) < 2 || args[1] != kittyOutputHelperArgument {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("invalid image output helper invocation")
	}
	if err := kittyReadPipe(3); err != nil {
		return true, err
	}
	socketType, err := unix.GetsockoptInt(4, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || socketType != unix.SOCK_STREAM {
		return true, errors.New("image output helper requires a control socket")
	}
	address, err := unix.Getsockname(4)
	if _, ok := address.(*unix.SockaddrUnix); err != nil || !ok {
		return true, errors.New("image output helper requires a Unix socket")
	}
	if _, err := unix.Getpeername(4); err != nil {
		return true, errors.New("image output helper requires a connected control socket")
	}
	unix.CloseOnExec(3)
	unix.CloseOnExec(4)
	if err := unix.SetNonblock(3, true); err != nil {
		return true, err
	}
	life := os.NewFile(3, "image-output-lifeline")
	defer life.Close()
	control, err := kittyControl(os.NewFile(4, "image-output-control"))
	if err != nil {
		return true, err
	}
	defer control.Close()
	if err := kittyInterruptDisposition(true); err != nil {
		return true, err
	}
	// Only the helper uses process exit: it has no children or user state to
	// unwind. This stops a blocked terminal syscall when the parent disappears.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		var value [1]byte
		_, _ = life.Read(value[:])
		select {
		case <-finished:
			return
		default:
			os.Exit(0)
		}
	}()
	if _, _, err := control.WriteMsgUnix([]byte{'R'}, nil, nil); err != nil {
		return true, err
	}
	for {
		command, descriptors, err := readKittyControl(control)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return true, nil
			}
			return true, err
		}
		if command != 'J' || len(descriptors) != 1 {
			closeKittyDescriptors(descriptors)
			return true, errors.New("invalid image output job")
		}
		jobErr := runKittyJob(control, descriptors[0])
		status := byte('0')
		if jobErr != nil {
			status = '1'
		}
		if _, _, err := control.WriteMsgUnix([]byte{status}, nil, nil); err != nil {
			return true, err
		}
	}
}

func runKittyJob(control *net.UnixConn, fd int) error {
	input := os.NewFile(uintptr(fd), "image-output-data")
	defer input.Close()
	if err := kittyReadPipe(fd); err != nil {
		return err
	}
	// Go's SIGINT handler can resume writes after the terminal flushes their
	// headers. Use the kernel's default disposition only inside this worker.
	// No data is sent until the parent receives this readiness acknowledgement.
	if err := kittyInterruptDisposition(false); err != nil {
		return err
	}
	_, _, readyErr := control.WriteMsgUnix([]byte{'A'}, nil, nil)
	var writeErr error
	if readyErr == nil {
		_, writeErr = io.Copy(os.Stdout, input)
	}
	// Ignore only while idle, including the spare reserved for protocol reset.
	// Restoring a Go handler/trampoline is unnecessary and deliberately avoided.
	return errors.Join(readyErr, writeErr, kittyInterruptDisposition(true))
}
