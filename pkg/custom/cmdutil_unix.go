//go:build !windows

package custom

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	clioutput "github.com/openai/openai-cli/internal/output"
	"golang.org/x/sys/unix"
)

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

	outputErr := generateOutput(pagerInput)

	// Close the file NOW before we wait for the child process to terminate.
	// This way, the child will receive the end-of-file signal and know that
	// there is no more input. Otherwise the child process may block
	// indefinitely waiting for another line (this can happen when streaming
	// less than a screenful of data to a pager).
	pagerInput.Close()

	// Wait for child process to exit
	var wstatus syscall.WaitStatus
	_, err = syscall.Wait4(pid, &wstatus, 0, nil)
	// Preserve generation errors, but only after closing and reaping the pager.
	if outputErr != nil && !clioutput.IsBrokenPipe(outputErr) {
		return outputErr
	}
	if wstatus.ExitStatus() != 0 {
		return wrapPagerError(fmt.Errorf("Pager exited with non-zero exit status: %d", wstatus.ExitStatus()))
	}
	return wrapPagerError(err)
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

	command := pagerCommand()

	pagerPath, err := exec.LookPath(command[0])
	if err != nil {
		parentConn.Close()
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

	pid, err := syscall.ForkExec(pagerPath, command, procAttr)
	if err != nil {
		parentConn.Close()
		return nil, 0, err
	}

	return parentConn, pid, nil
}
