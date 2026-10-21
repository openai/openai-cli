//go:build !windows

// Package socket provides borrowed-descriptor fixtures for input contracts.
package socket

import (
	"golang.org/x/sys/unix"
	"os"
	"testing"
	"time"
)

// Pair creates a socket pair with the requested borrowed-input descriptor mode.
func Pair(t *testing.T, blocking bool) (*os.File, *os.File) {
	t.Helper()
	descriptors, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		unix.CloseOnExec(descriptor)
	}
	// Initialize os.File's poll descriptor before modeling an inherited blocking
	// socket. This preserves caller deadline support in both fixture modes.
	if err := unix.SetNonblock(descriptors[0], true); err != nil {
		unix.Close(descriptors[0])
		unix.Close(descriptors[1])
		t.Fatal(err)
	}
	input := os.NewFile(uintptr(descriptors[0]), "synthetic-batch-socket-input")
	producer := os.NewFile(uintptr(descriptors[1]), "synthetic-batch-socket-producer")
	t.Cleanup(func() {
		producer.Close()
		input.Close()
	})
	if blocking {
		connection, err := input.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var modeErr error
		if err := connection.Control(func(fd uintptr) {
			modeErr = unix.SetNonblock(int(fd), false)
		}); err != nil || modeErr != nil {
			t.Fatalf("prepare blocking socket: %v / %v", err, modeErr)
		}
	}
	if nonblocking := Flags(t, input)&unix.O_NONBLOCK != 0; nonblocking == blocking {
		t.Fatal("socket fixture did not establish its requested descriptor mode")
	}
	return input, producer
}

// AwaitRead waits for cancellation and releases the owned peer on watchdog expiry.
func AwaitRead(t *testing.T, producer *os.File, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		// Closing the owned peer releases even the prior synchronous read path.
		producer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("socket reader remained active after fixture cleanup")
		}
		t.Fatal("socket read watchdog expired before cancellation or caller deadline")
		return nil
	}
}

// Flags reads descriptor flags without changing caller ownership.
func Flags(t *testing.T, file *os.File) int {
	t.Helper()
	connection, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	flags := 0
	var flagErr error
	if err := connection.Control(func(fd uintptr) {
		flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
	}); err != nil || flagErr != nil {
		t.Fatalf("read descriptor flags: %v / %v", err, flagErr)
	}
	return flags
}
