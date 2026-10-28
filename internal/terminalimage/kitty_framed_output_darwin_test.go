package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestKittyTTYFrameNeverResumesPartialWrite(t *testing.T) {
	frame := []byte("\x1b_Gq=2,m=1;YWJj\x1b\\")
	for n := 0; n < len(frame); n++ {
		for _, cause := range []error{nil, unix.EAGAIN, unix.EINTR, unix.EIO} {
			if n == 0 && (cause == unix.EAGAIN || cause == unix.EINTR) {
				continue // Zero-byte interruptions are safe to retry in full.
			}
			calls, polls := 0, 0
			err := writeKittyTTYFrame(frame, func() error { polls++; return nil }, func(data []byte) (int, error) {
				calls++
				require.Equal(t, frame, data)
				return n, cause
			})
			require.Error(t, err)
			require.Equal(t, 1, calls, "never resume a possibly flushed payload tail")
			require.Equal(t, 1, polls)
			if n > 0 || cause == nil {
				require.ErrorIs(t, err, io.ErrShortWrite)
			}
		}
	}
}

func TestKittyTTYFramePollsBeforeEveryAttempt(t *testing.T) {
	frame := []byte("\x1b_Gq=2,m=0;YWJj\x1b\\")
	var events []string
	calls := 0
	write := func(data []byte) (int, error) {
		events = append(events, "write")
		require.Equal(t, frame, data)
		calls++
		switch calls {
		case 1:
			return -1, unix.EAGAIN
		case 2:
			return -1, unix.EINTR
		default:
			return len(data), nil
		}
	}
	ready := func() error { events = append(events, "poll"); return nil }
	require.NoError(t, writeKittyTTYFrame(frame, ready, write))
	require.NoError(t, writeKittyTTYFrame(frame, ready, write))
	require.Equal(t, []string{"poll", "write", "poll", "write", "poll", "write", "poll", "write"}, events)
	cause := errors.New("terminal hung up")
	require.ErrorIs(t, writeKittyTTYFrame(frame, func() error { return cause }, func([]byte) (int, error) {
		t.Fatal("readiness failure must prevent writing")
		return 0, nil
	}), cause)
}

func kittyPrivateTTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	require.NoError(t, err)
	master := os.NewFile(uintptr(fd), "owned-kitty-test-pty")
	t.Cleanup(func() { master.Close() })
	require.NoError(t, unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0))
	require.NoError(t, unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0))
	var path [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&path[0])))
	require.Zero(t, errno)
	end := bytes.IndexByte(path[:], 0)
	require.Positive(t, end)
	slaveFD, err := unix.Open(string(path[:end]), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	require.NoError(t, err)
	slave := os.NewFile(uintptr(slaveFD), string(path[:end]))
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func TestKittyTTYOwnsDescriptorFlags(t *testing.T) {
	_, slave := kittyPrivateTTY(t)
	flags, err := unix.FcntlInt(slave.Fd(), unix.F_GETFL, 0)
	require.NoError(t, err)
	settings, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TIOCGETA)
	require.NoError(t, err)
	owned, err := openKittyTTY(slave)
	require.NoError(t, err)
	defer unix.Close(owned)
	ownedFlags, err := unix.FcntlInt(uintptr(owned), unix.F_GETFL, 0)
	require.NoError(t, err)
	require.NotZero(t, ownedFlags&unix.O_NONBLOCK)
	for _, nonblocking := range []bool{false, true} {
		require.NoError(t, unix.SetNonblock(owned, nonblocking))
		after, err := unix.FcntlInt(slave.Fd(), unix.F_GETFL, 0)
		require.NoError(t, err)
		require.Equal(t, flags, after, "owned descriptor must not share stdout's flags")
	}
	afterSettings, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TIOCGETA)
	require.NoError(t, err)
	require.Equal(t, settings, afterSettings)
	readonly, err := os.Open(slave.Name())
	require.NoError(t, err)
	defer readonly.Close()
	_, err = openKittyTTY(readonly)
	require.Error(t, err, "reopening must not upgrade a read-only stdout")
}

func TestKittyFramedWorkerPreservesRecordsAndReuse(t *testing.T) {
	master, slave := kittyPrivateTTY(t)
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := startKittySession(ctx, executable, slave)
	require.NoError(t, err)
	defer session.Close()
	frames := []string{"\x1b_Gq=2,m=1;YWJj\x1b\\", "\x1b_Gq=2,m=0;ZGVm\x1b\\", "\x00\x1b\\\x1b_Gq=2,m=0;\x1b\\"}
	var expected bytes.Buffer
	for _, frame := range frames {
		written, err := session.writeJob(ctx, 'K', func(out io.Writer) error {
			_, err := io.WriteString(out, frame)
			return err
		})
		require.NoError(t, err)
		require.True(t, written)
		expected.WriteString(frame)
	}
	require.NoError(t, session.Close())
	ready := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
	n, err := unix.Poll(ready, 1000)
	require.NoError(t, err)
	require.Positive(t, n)
	got := make([]byte, expected.Len())
	n, err = unix.Read(int(master.Fd()), got)
	require.NoError(t, err)
	require.Equal(t, expected.Len(), n)
	require.Equal(t, expected.Bytes(), got, "private record lengths must never reach stdout")
	for _, worker := range session.workers {
		require.NotNil(t, worker.command.ProcessState, "reap each directly owned worker")
	}
}
