//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

const kittyOutputHelperArgument = "__image-output"

// RunKittyOutputHelper handles only the private output-supervisor invocation.
// Call it before normal command setup. It never reads the user's terminal or
// changes the calling CLI's signal handlers: it runs in a dedicated process.
func RunKittyOutputHelper(args []string) (bool, error) {
	if len(args) < 2 || args[1] != kittyOutputHelperArgument {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("invalid image output helper invocation")
	}
	for _, fd := range []uintptr{os.Stdin.Fd(), 3} {
		var info unix.Stat_t
		if err := unix.Fstat(int(fd), &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFIFO {
			return true, errors.New("image output helper requires pipe descriptors")
		}
		flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
		if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
			return true, errors.New("image output helper requires read-only pipes")
		}
	}
	// This is our own pipe, not a user terminal descriptor. Making it pollable
	// lets Close interrupt its reader after cat finishes normally.
	if err := unix.SetNonblock(3, true); err != nil {
		return true, errors.New("missing image output lifeline")
	}
	life := os.NewFile(3, "image-output-lifeline")
	if life == nil {
		return true, errors.New("missing image output lifeline")
	}
	defer life.Close()
	// Do not give cat a copy of the lifeline. Only the parent owns its writer;
	// parent exit, including SIGKILL, must make the supervisor's read return.
	syscall.CloseOnExec(3)
	path, err := kittyCatPath()
	if err != nil {
		return true, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Check an already-closed lifeline before starting cat. Reading a live
	// lifeline would block; poll does not change its descriptor flags.
	fds := []unix.PollFd{{Fd: 3, Events: unix.POLLIN | unix.POLLHUP}}
	if _, err := unix.Poll(fds, 0); err != nil {
		return true, fmt.Errorf("check image output lifeline: %w", err)
	}
	if fds[0].Revents != 0 {
		return true, context.Canceled
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var value [1]byte
		_, _ = life.Read(value[:])
		cancel()
	}()
	defer func() {
		_ = life.Close()
		<-done
	}()
	copier := exec.CommandContext(ctx, path)
	copier.Stdin = os.Stdin
	copier.Stdout = os.Stdout
	// Only native cat writes graphics. It receives keyboard signals in the
	// same foreground group; the supervisor also reaps it after parent death.
	if err := copier.Run(); err != nil {
		return true, err
	}
	return true, ctx.Err()
}
