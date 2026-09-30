//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

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
// Its own signal handlers and owned pipes never alter the parent CLI's input.
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
	path, err := kittyCatPath()
	if err != nil {
		return true, err
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
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { cancel(); life.Close(); control.Close(); workers.Wait() }()
	workers.Go(func() { var value [1]byte; _, _ = life.Read(value[:]); cancel(); control.Close() })
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)
	workers.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case received := <-signals:
				// Native cat receives foreground SIGINT itself. Drain the
				// supervisor's copy without routing a delayed signal to the
				// next job (particularly the interrupted frame's cleanup).
				if received != syscall.SIGINT {
					cancel()
					control.Close()
					return
				}
			}
		}
	})
	if _, _, err := control.WriteMsgUnix([]byte{'R'}, nil, nil); err != nil {
		return true, err
	}
	for {
		command, descriptors, err := readKittyControl(control)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return true, nil
			}
			return true, err
		}
		if command != 'J' || len(descriptors) != 2 {
			closeKittyDescriptors(descriptors)
			return true, errors.New("invalid image output job")
		}
		jobCtx, jobCancel := context.WithCancel(ctx)
		jobErr := runKittyJob(jobCtx, path, descriptors)
		jobCancel()
		status := byte('0')
		if jobErr != nil {
			status = '1'
		}
		if _, _, err := control.WriteMsgUnix([]byte{status}, nil, nil); err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return true, nil
			}
			return true, err
		}
	}
}

func runKittyJob(ctx context.Context, path string, fds []int) error {
	for _, fd := range fds {
		if err := kittyReadPipe(fd); err != nil {
			closeKittyDescriptors(fds)
			return err
		}
	}
	var dataInfo, lifeInfo unix.Stat_t
	if err := unix.Fstat(fds[0], &dataInfo); err != nil {
		closeKittyDescriptors(fds)
		return err
	}
	if err := unix.Fstat(fds[1], &lifeInfo); err != nil {
		closeKittyDescriptors(fds)
		return err
	}
	if dataInfo.Dev == lifeInfo.Dev && dataInfo.Ino == lifeInfo.Ino {
		closeKittyDescriptors(fds)
		return errors.New("image data and cancellation require separate pipes")
	}
	if err := unix.SetNonblock(fds[1], true); err != nil {
		closeKittyDescriptors(fds)
		return err
	}
	input := os.NewFile(uintptr(fds[0]), "image-output-data")
	life := os.NewFile(uintptr(fds[1]), "image-output-cancel")
	defer input.Close()
	defer life.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); var value [1]byte; _, _ = life.Read(value[:]); cancel() }()
	defer func() { life.Close(); <-done }()
	// A job canceled before receipt must not start a native terminal writer.
	ready := []unix.PollFd{{Fd: int32(fds[1]), Events: unix.POLLIN | unix.POLLHUP}}
	if _, err := unix.Poll(ready, 0); err != nil {
		return err
	}
	if ready[0].Revents != 0 {
		return context.Canceled
	}
	cat := exec.CommandContext(ctx, path)
	cat.Stdin = input
	cat.Stdout = os.Stdout
	// Same foreground group, ordinary native signal semantics. Wait reaps cat
	// before the job ACK lets parent labels or a cleanup job reach the terminal.
	return cat.Run()
}
