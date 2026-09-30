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
	"time"

	"golang.org/x/sys/unix"
)

type kittyOutputSession struct {
	command   *exec.Cmd
	control   *net.UnixConn
	life      *os.File
	gate      chan struct{}
	used      atomic.Bool
	closeOnce sync.Once
	closeErr  error
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

func startKittySession(ctx context.Context, path string, out *os.File) (*kittyOutputSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := kittyCatPath(); err != nil {
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
	session := &kittyOutputSession{command: command, control: control, life: lifeWrite, gate: make(chan struct{}, 1)}
	session.gate <- struct{}{}
	startup, cancelStartup := context.WithTimeout(ctx, 2*time.Second)
	defer cancelStartup()
	stop := context.AfterFunc(startup, func() { lifeWrite.Close(); control.Close() })
	ready, descriptors, err := readKittyControl(control)
	closeKittyDescriptors(descriptors)
	stop()
	if err != nil || ready != 'R' || len(descriptors) != 0 || startup.Err() != nil {
		// No job was sent. An incompatible or stopped executable cannot be
		// trusted to honor the lifeline, and has no native writer to orphan.
		_ = command.Process.Kill()
		return nil, errors.Join(errors.New("native image output helper did not become ready"), err, startup.Err(), session.Close())
	}
	return session, nil
}

func (s *kittyOutputSession) Close() error {
	s.closeOnce.Do(func() {
		_ = s.life.Close()
		_ = s.control.Close()
		s.closeErr = kittyWaitError(s.command.Wait())
	})
	return s.closeErr
}

func kittyWaitError(err error) error {
	if err == nil {
		return nil
	}
	if _, exited := err.(*exec.ExitError); exited {
		return fmt.Errorf("write image to terminal: %v", err)
	}
	return fmt.Errorf("write image to terminal: %w", err)
}

func (s *kittyOutputSession) write(ctx context.Context, write func(io.Writer) error) (written bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dataRead, dataWrite, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer dataRead.Close()
	defer dataWrite.Close()
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer lifeRead.Close()
	defer lifeWrite.Close()
	_, _, err = s.control.WriteMsgUnix([]byte{'J'}, unix.UnixRights(int(dataRead.Fd()), int(lifeRead.Fd())), nil)
	if err != nil {
		return false, err
	}
	dataRead.Close()
	lifeRead.Close()
	stop := context.AfterFunc(ctx, func() { lifeWrite.Close() })
	defer stop()
	acknowledged := false
	defer func() {
		if !acknowledged {
			// A callback panic still cancels and waits for the owned native writer.
			lifeWrite.Close()
			dataWrite.Close()
			_, fds, _ := readKittyControl(s.control)
			closeKittyDescriptors(fds)
		}
	}()
	writer := &kittyPipeWriter{Writer: dataWrite}
	writeErr := write(writer)
	closeErr := dataWrite.Close()
	if writeErr != nil {
		lifeWrite.Close()
	}
	status, fds, readErr := readKittyControl(s.control)
	closeKittyDescriptors(fds)
	acknowledged = true
	var outputErr error
	if status != '0' || len(fds) != 0 {
		outputErr = errors.New("write image to terminal: native writer failed")
	}
	return writer.written, errors.Join(writeErr, closeErr, readErr, outputErr, ctx.Err())
}

func closeKittyDescriptors(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}

// Only control bytes and at most two owned pipe descriptors belong here. Keep
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
	if len(fds) > 2 {
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
