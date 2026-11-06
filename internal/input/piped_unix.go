//go:build !windows

package input

import (
	"os"

	"golang.org/x/sys/unix"
)

func pipedDataAvailable(file *os.File) bool {
	// Try to determine if there's non-empty data being piped into the command by polling for data for a short
	// amount of time. This is necessary because some environments (e.g. Cursor's integrated terminal) connect
	// stdin as a pipe even when nothing is being piped, which would cause the command to block indefinitely
	// waiting for input that will never come. The 10 ms timeout is arbitrary -- designed to be long enough to
	// allow data to be detected, but short enough that it shouldn't cause a noticeable delay in command runs.
	fds := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	n, _ := unix.Poll(fds, 10 /* ms */)
	return n > 0
}
