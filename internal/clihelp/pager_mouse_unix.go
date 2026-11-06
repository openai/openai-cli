//go:build unix

package clihelp

import (
	"context"
	"os"
	"time"

	clioutput "github.com/openai/openai-cli/internal/output"
	"golang.org/x/sys/unix"
)

// Wait for the terminal to process the preceding mouse reset before restoring
// cooked input. The terminal writes its status response after its queued mouse
// reports. Read precisely through that response, never into following shell input.
func finishHelpMouseInput(in, out *os.File) (interrupted bool, err error) {
	inputInfo, inputErr := in.Stat()
	outputInfo, outputErr := out.Stat()
	if inputErr != nil || outputErr != nil || !os.SameFile(inputInfo, outputInfo) {
		return interrupted, nil
	}
	if _, err := (clioutput.Writer{Context: context.Background(), Sink: out}).WriteString("\x1b[5n"); err != nil {
		return interrupted, err
	}
	// A proxy, minimal terminal, or detached emulator may omit the response.
	const idle = 200 * time.Millisecond
	hardDeadline := time.Now().Add(2 * time.Second)
	deadline := time.Now().Add(idle)
	const response = "\x1b[0n"
	matched := 0
	poll := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
	var buf [len(response)]byte
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return interrupted, nil
		}
		n, err := unix.Poll(poll, int(left.Milliseconds())+1)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 || poll[0].Revents&unix.POLLIN == 0 {
			return interrupted, err
		}
		// The earliest possible response ends at its remaining length. Even
		// if a command follows in the same packet, it stays in the tty.
		count, err := unix.Read(int(in.Fd()), buf[:len(response)-matched])
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil || count == 0 {
			return interrupted, err
		}
		for _, b := range buf[:count] {
			if b == 0x03 {
				interrupted = true
			}
			if b == response[matched] {
				matched++
				if matched == len(response) {
					return interrupted, nil
				}
			} else if b == response[0] {
				matched = 1
			} else {
				matched = 0
			}
		}
		deadline = time.Now().Add(idle)
		if deadline.After(hardDeadline) {
			deadline = hardDeadline
		}
	}
}
