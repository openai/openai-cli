//go:build unix

package clihelp

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	clioutput "github.com/openai/openai-cli/internal/output"
	"golang.org/x/sys/unix"
)

// A killed Tea program cancels its reader without waiting for its last poll.
// Revoke that reader before asking for a terminal reply. Otherwise it can
// consume both the reply and a shell command delivered in the same write.
type helpProgramInput struct {
	*os.File
	mu     sync.Mutex
	source io.Reader
}

func ownHelpProgramInput(tty *os.File, source io.Reader) (io.Reader, func()) {
	input := &helpProgramInput{File: tty, source: source}
	return input, func() {
		input.mu.Lock()
		input.source = nil
		input.mu.Unlock()
	}
}

func (r *helpProgramInput) Read(buf []byte) (int, error) {
	// Tea's cancelable Unix reader polls the file before calling Read.
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.source == nil {
		return 0, io.EOF
	}
	return r.source.Read(buf)
}

// Wait for the terminal to process the preceding mouse reset before restoring
// cooked input. The terminal writes its status response after its queued mouse
// reports. Read precisely through that response, never into following shell input.
func finishHelpMouseInput(in, out *os.File, draft *helpDraft, aborting bool) (interrupted bool, err error) {
	inputInfo, inputErr := in.Stat()
	outputInfo, outputErr := out.Stat()
	if inputErr != nil || outputErr != nil || !os.SameFile(inputInfo, outputInfo) {
		return interrupted, nil
	}
	guardPaste := draft != nil && draft.enabled
	if guardPaste {
		defer func() {
			if !draft.settled {
				draft.recovery = draft.input.recoveryTag()
			}
		}()
		if draft.settled || draft.input.receivedReply() {
			return false, nil
		}
		if draft.started.IsZero() {
			if err := draft.input.requestReply(); err != nil {
				return false, err
			}
		}
	}
	if !guardPaste {
		if _, err := (clioutput.Writer{Context: context.Background(), Sink: out}).WriteString("\x1b[5n"); err != nil {
			return false, err
		}
	}
	// A proxy, minimal terminal, or detached emulator may omit the response.
	const idle = 200 * time.Millisecond
	hardDeadline := time.Now().Add(2 * time.Second)
	deadline := time.Now().Add(idle)
	const response = "\x1b[0n"
	matched := 0
	poll := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
	var buf [4096]byte
	for {
		left := time.Until(deadline)
		if left <= 0 {
			if guardPaste && aborting && time.Now().After(hardDeadline) {
				return interrupted, nil
			}
			if !guardPaste || !draft.input.pasteInProgress() {
				return interrupted, nil
			}
			// Releasing an incomplete paste could submit its remaining lines to zsh.
			left = idle
		}
		n, err := unix.Poll(poll, int(left.Milliseconds())+1)
		if err == unix.EINTR {
			continue
		}
		if n == 0 && guardPaste && draft.input.pasteInProgress() && err == nil {
			continue
		}
		if err != nil || n == 0 || poll[0].Revents&unix.POLLIN == 0 {
			return interrupted, err
		}
		// The earliest possible response ends at its remaining length. Even
		// if a command follows in the same packet, it stays in the tty.
		var count int
		if guardPaste {
			count, err = draft.input.Read(buf[:])
			if draft.input.receivedReply() {
				return interrupted, nil
			}
			if err == nil && count == 0 {
				continue
			}
		} else {
			count, err = unix.Read(int(in.Fd()), buf[:len(response)-matched])
		}
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
			if guardPaste {
				continue
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
