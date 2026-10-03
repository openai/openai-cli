//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type kittyOutputWorker struct {
	command   *exec.Cmd
	control   *net.UnixConn
	life      *os.File
	used      atomic.Bool
	retired   atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// A spare prepared before input survives SIGINT while idle and can terminate an
// interrupted frame even if the executable is removed or replaced later.
// A failed worker is retired; normal jobs reuse the current live worker.
type kittyOutputSession struct {
	workers []*kittyOutputWorker
	gate    chan struct{}
	used    atomic.Bool
}

func startKittySession(ctx context.Context, path string, out *os.File) (*kittyOutputSession, error) {
	s := &kittyOutputSession{gate: make(chan struct{}, 1)}
	s.gate <- struct{}{}
	for range 2 {
		worker, err := startKittyWorker(ctx, path, out)
		if err != nil {
			return nil, errors.Join(err, s.Close())
		}
		s.workers = append(s.workers, worker)
	}
	return s, nil
}

func (s *kittyOutputSession) Close() error {
	errs := make([]error, len(s.workers))
	var closing sync.WaitGroup
	for i, worker := range s.workers {
		closing.Go(func() { errs[i] = worker.Close() })
	}
	closing.Wait()
	var err error
	for i, worker := range s.workers {
		// Retired workers were already reaped and reported by write, including
		// intentional cancellation. Do not poison a successful reset with it.
		// An unused reserve cannot affect output that completed elsewhere.
		if worker.used.Load() && !worker.retired.Load() {
			err = errors.Join(err, errs[i])
		}
	}
	return err
}

func (s *kittyOutputSession) write(ctx context.Context, write func(io.Writer) error) (bool, error) {
	for _, worker := range s.workers {
		if !worker.retired.Load() {
			return worker.write(ctx, write)
		}
	}
	return false, errors.New("native image output workers have stopped")
}

func kittyControl(file *os.File) (*net.UnixConn, error) {
	connection, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	control, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, errors.New("image output requires a Unix socket")
	}
	return control, nil
}

func startKittyWorker(ctx context.Context, path string, out *os.File) (*kittyOutputWorker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer lifeRead.Close()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		lifeWrite.Close()
		return nil, err
	}
	unix.CloseOnExec(pair[0])
	unix.CloseOnExec(pair[1])
	parent := os.NewFile(uintptr(pair[0]), "image-control-parent")
	child := os.NewFile(uintptr(pair[1]), "image-control-child")
	defer child.Close()
	control, err := kittyControl(parent)
	if err != nil {
		lifeWrite.Close()
		return nil, err
	}
	command := exec.Command(path, kittyOutputHelperArgument)
	command.Stdout = out
	command.ExtraFiles = []*os.File{lifeRead, child}
	if err := command.Start(); err != nil {
		control.Close()
		lifeWrite.Close()
		return nil, fmt.Errorf("start native image output: %w", err)
	}
	lifeRead.Close()
	child.Close()
	session := &kittyOutputWorker{command: command, control: control, life: lifeWrite}
	startup, cancelStartup := context.WithTimeout(ctx, 2*time.Second)
	defer cancelStartup()
	stopped := make(chan struct{})
	stop := context.AfterFunc(startup, func() { defer close(stopped); lifeWrite.Close(); control.Close() })
	ready, descriptors, err := readKittyControl(control)
	closeKittyDescriptors(descriptors)
	if !stop() {
		<-stopped
	}
	if err != nil || ready != 'R' || len(descriptors) != 0 || startup.Err() != nil {
		// No job was sent. An incompatible or stopped executable cannot be
		// trusted to honor the lifeline, and has no native writer to orphan.
		_ = command.Process.Kill()
		return nil, errors.Join(errors.New("native image output helper did not become ready"), err, startup.Err(), session.Close())
	}
	return session, nil
}

func (s *kittyOutputWorker) Close() error {
	s.closeOnce.Do(func() {
		_ = s.life.Close()
		_ = s.control.Close()
		// A stopped or unresponsive worker cannot read its lifeline. Bound
		// shutdown only; ordinary image output has no timeout.
		stopped := make(chan struct{})
		timer := time.AfterFunc(kittyOutputCleanupTimeout, func() {
			defer close(stopped)
			_ = s.command.Process.Kill()
		})
		s.closeErr = kittyWaitError(s.command.Wait())
		if !timer.Stop() {
			<-stopped
		}
	})
	return s.closeErr
}

func kittyWaitError(err error) error {
	if err == nil {
		return nil
	}
	if exit, exited := err.(*exec.ExitError); exited {
		// The worker can observe foreground SIGINT before the parent's Go
		// cancellation goroutine. Preserve cancellation classification then.
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGINT {
			return context.Canceled
		}
		return fmt.Errorf("write image to terminal: %v", err)
	}
	return fmt.Errorf("write image to terminal: %w", err)
}

func (s *kittyOutputWorker) retire() {
	s.retired.Store(true)
	_ = s.command.Process.Kill()
	_ = s.control.Close()
}

func (s *kittyOutputWorker) write(ctx context.Context, write func(io.Writer) error) (written bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dataRead, dataWrite, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer dataRead.Close()
	defer dataWrite.Close()
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(canceled)
		s.retire()
		_ = dataWrite.Close()
	})
	var done chan error
	acknowledged := false
	completed := false
	defer func() {
		// A callback that has started must finish before reusing any worker.
		if !stop() {
			<-canceled
		}
		err = errors.Join(err, ctx.Err())
		if !completed || err != nil {
			s.retire()
			_ = dataWrite.Close()
			if done != nil && !acknowledged {
				<-done
			}
			// Kill and Wait refer to this directly owned child. No descendant
			// can remain writing after we return to text or the reset worker.
			err = errors.Join(err, s.Close())
		}
	}()
	s.used.Store(true)
	_, _, err = s.control.WriteMsgUnix([]byte{'J'}, unix.UnixRights(int(dataRead.Fd())), nil)
	if err != nil {
		return false, err
	}
	dataRead.Close()
	ready, fds, readyErr := readKittyControl(s.control)
	closeKittyDescriptors(fds)
	if readyErr != nil || ready != 'A' || len(fds) != 0 {
		return false, errors.Join(errors.New("native image writer did not become ready"), readyErr)
	}
	done = make(chan error, 1)
	go func() {
		status, fds, readErr := readKittyControl(s.control)
		closeKittyDescriptors(fds)
		if status != '0' || len(fds) != 0 {
			readErr = errors.Join(readErr, errors.New("write image to terminal: native writer failed"))
		}
		if readErr != nil {
			s.retire()
			_ = dataWrite.Close()
		}
		done <- readErr
	}()
	writer := &kittyPipeWriter{Writer: dataWrite}
	writeErr := write(writer)
	if writeErr != nil {
		s.retire()
	}
	closeErr := dataWrite.Close()
	ack := <-done
	acknowledged = true
	completed = true
	return writer.written, errors.Join(writeErr, closeErr, ack)
}

func closeKittyDescriptors(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}

// Only control bytes and at most one owned pipe descriptor belong here. Keep
// ancillary storage bounded and close all delivered descriptors on rejection.
func readKittyControl(control *net.UnixConn) (command byte, fds []int, err error) {
	var data [1]byte
	// Reserve above Darwin's 512-FD and Linux's 253-FD SCM_RIGHTS limits.
	// On Darwin, truncation can install FDs whose numbers are not returned.
	ancillary := make([]byte, 4096)
	n, oobn, flags, _, readErr := control.ReadMsgUnix(data[:], ancillary)
	remaining := ancillary[:oobn]
	for len(remaining) >= unix.CmsgLen(0) {
		header, payload, rest, parseErr := unix.ParseOneSocketControlMessage(remaining)
		if parseErr != nil {
			err = errors.Join(err, parseErr)
			break
		}
		remaining = rest
		if header.Level != unix.SOL_SOCKET || header.Type != unix.SCM_RIGHTS {
			err = errors.Join(err, errors.New("unexpected image control metadata"))
			continue
		}
		if len(payload)%4 != 0 {
			err = errors.Join(err, errors.New("invalid image control descriptors"))
			payload = payload[:len(payload)/4*4]
		}
		rights, parseErr := unix.ParseUnixRights(&unix.SocketControlMessage{Header: header, Data: payload})
		err = errors.Join(err, parseErr)
		for _, fd := range rights {
			unix.CloseOnExec(fd)
		}
		fds = append(fds, rights...)
	}
	if len(fds) > 1 {
		err = errors.Join(err, errors.New("too many image control descriptors"))
	}
	if n == 0 {
		err = errors.Join(err, io.EOF)
	} else if n != 1 {
		err = errors.Join(err, io.ErrUnexpectedEOF)
	}
	if flags&unix.MSG_CTRUNC != 0 {
		err = errors.Join(err, io.ErrUnexpectedEOF)
	}
	err = errors.Join(err, readErr)
	if err != nil {
		closeKittyDescriptors(fds)
		return 0, nil, err
	}
	return data[0], fds, nil
}
