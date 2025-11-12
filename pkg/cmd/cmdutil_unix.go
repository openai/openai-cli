//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func isPipedDataAvailableOSSpecific() bool {
	// Try to determine if there's non-empty data being piped into the command by polling for data for a short
	// amount of time. This is necessary because some environments (e.g. Cursor's integrated terminal) connect
	// stdin as a pipe even when nothing is being piped, which would cause the command to block indefinitely
	// waiting for input that will never come. The 10 ms timeout is arbitrary -- designed to be long enough to
	// allow data to be detected, but short enough that it shouldn't cause a noticeable delay in command runs.
	fds := []unix.PollFd{{Fd: int32(os.Stdin.Fd()), Events: unix.POLLIN}}
	n, _ := unix.Poll(fds, 10 /* ms */)
	return n > 0
}

func streamOutputOSSpecific(label string, generateOutput func(w *os.File) error) error {
	// Try to use socket pair for better buffer control
	pagerInput, pid, err := openSocketPairPager(label)
	if err != nil || pagerInput == nil {
		// Fall back to pipe if socket setup fails
		return streamToPagerWithPipe(label, generateOutput)
	}
	defer pagerInput.Close()

	// If we would be streaming to a terminal and aren't forcing color one way
	// or the other, we should configure things to use color so the pager gets
	// colorized input.
	if isTerminal(os.Stdout) && os.Getenv("FORCE_COLOR") == "" {
		os.Setenv("FORCE_COLOR", "1")
	}

	// If the pager exits before reading all input, then generateOutput() will
	// produce a broken pipe error, which is fine and we don't want to propagate it.
	if err := generateOutput(pagerInput); err != nil &&
		!strings.Contains(err.Error(), "broken pipe") {
		return err
	}

	// Close the file NOW before we wait for the child process to terminate.
	// This way, the child will receive the end-of-file signal and know that
	// there is no more input. Otherwise the child process may block
	// indefinitely waiting for another line (this can happen when streaming
	// less than a screenful of data to a pager).
	pagerInput.Close()

	// Wait for child process to exit
	var wstatus syscall.WaitStatus
	_, err = syscall.Wait4(pid, &wstatus, 0, nil)
	if wstatus.ExitStatus() != 0 {
		return fmt.Errorf("Pager exited with non-zero exit status: %d", wstatus.ExitStatus())
	}
	return err
}

func openSocketPairPager(label string) (*os.File, int, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, 0, err
	}

	// The child file descriptor will be sent to the child process through
	// ProcAttr and ForkExec(), while the parent process will always close the
	// child file descriptor.
	// The parent file descriptor will be wrapped in an os.File wrapper and
	// returned from this function, or closed if something goes wrong.
	parentFd, childFd := fds[0], fds[1]
	defer unix.Close(childFd)

	// Use small buffer sizes so we don't ask the server for more paginated
	// values than we actually need.
	if err := unix.SetsockoptInt(parentFd, unix.SOL_SOCKET, unix.SO_SNDBUF, 128); err != nil {
		unix.Close(parentFd)
		return nil, 0, err
	}
	if err := unix.SetsockoptInt(childFd, unix.SOL_SOCKET, unix.SO_RCVBUF, 128); err != nil {
		unix.Close(parentFd)
		return nil, 0, err
	}

	// Set CLOEXEC on the parent file descriptor so it doesn't leak to child
	syscall.CloseOnExec(parentFd)

	parentConn := os.NewFile(uintptr(parentFd), "parent-socket")

	pagerProgram := os.Getenv("PAGER")
	if pagerProgram == "" {
		pagerProgram = "less"
	}

	pagerPath, err := exec.LookPath(pagerProgram)
	if err != nil {
		unix.Close(parentFd)
		return nil, 0, err
	}

	env := os.Environ()
	env = append(env, "LESS=-r -P "+label)
	env = append(env, "MORE=-r -P "+label)

	procAttr := &syscall.ProcAttr{
		Dir: "",
		Env: env,
		Files: []uintptr{
			uintptr(childFd),        // stdin (fd 0)
			uintptr(syscall.Stdout), // stdout (fd 1)
			uintptr(syscall.Stderr), // stderr (fd 2)
		},
	}

	pid, err := syscall.ForkExec(pagerPath, []string{pagerProgram}, procAttr)
	if err != nil {
		unix.Close(parentFd)
		return nil, 0, err
	}

	return parentConn, pid, nil
}
