//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestKittySessionReusesResidentAfterJobCancellation(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	session, err := startKittySession(t.Context(), path, out)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithCancel(t.Context())
	_, err = session.write(ctx, func(w io.Writer) error {
		_, err := io.WriteString(w, "interrupted")
		cancel()
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	_, err = session.write(t.Context(), func(w io.Writer) error { _, err := io.WriteString(w, "cleanup"); return err })
	if err != nil {
		t.Fatalf("resident helper could not run cleanup: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil || len(data) < len("cleanup") || string(data[len(data)-len("cleanup"):]) != "cleanup" {
		t.Fatalf("cleanup was not complete before acknowledgement: %q, %v", data, err)
	}
}

func TestKittyReadinessCancellationReapsUnresponsiveExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unresponsive helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = startKittySession(ctx, path, out)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatalf("unready helper outlived cancellation: %v (%v)", err, time.Since(started))
	}
	var status interface{ ExitCode() int }
	if errors.As(err, &status) {
		t.Fatalf("helper ExitCoder escaped: %v", err)
	}
}

func TestKittyControlRejectsExcessDescriptorsWithoutLeaks(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	left, err := kittyControl(os.NewFile(uintptr(pair[0]), "left"))
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := kittyControl(os.NewFile(uintptr(pair[1]), "right"))
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	fds := make([]int, 64)
	for i := range fds {
		fds[i] = int(w.Fd())
	}
	if _, _, err := left.WriteMsgUnix([]byte{'J'}, unix.UnixRights(fds...), nil); err != nil {
		t.Fatal(err)
	}
	_, received, err := readKittyControl(right)
	if err == nil || len(received) != 0 {
		closeKittyDescriptors(received)
		t.Fatalf("excess control descriptors accepted: %v", err)
	}
	w.Close()
	if err := r.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := r.Read(b[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("transferred writer leaked after rejection: %v", err)
	}
}

func TestKittyJobRejectsAliasedPipesBeforeChangingFlags(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	one, err := unix.Dup(int(r.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	two, err := unix.Dup(int(r.Fd()))
	if err != nil {
		unix.Close(one)
		t.Fatal(err)
	}
	flags, err := unix.FcntlInt(r.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := runKittyJob(t.Context(), "/bin/cat", []int{one, two}); err == nil {
		t.Fatal("aliased data and cancellation pipes accepted")
	}
	after, err := unix.FcntlInt(r.Fd(), unix.F_GETFL, 0)
	if err != nil || after != flags {
		t.Fatalf("rejected pipe flags changed: %v, %v, %v", flags, after, err)
	}
}

func TestKittyHelperRejectsUnconnectedControlBeforeChangingFlags(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, listening := range []bool{false, true} {
		t.Run(fmt.Sprintf("listening-%v", listening), func(t *testing.T) {
			life, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer life.Close()
			defer write.Close()
			fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				t.Fatal(err)
			}
			socket := os.NewFile(uintptr(fd), "unconnected-control")
			defer socket.Close()
			if listening {
				directory, err := os.MkdirTemp("", "kitty-control-")
				if err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(directory)
				if err := unix.Bind(fd, &unix.SockaddrUnix{Name: filepath.Join(directory, "control")}); err != nil {
					t.Fatal(err)
				}
				if err := unix.Listen(fd, 1); err != nil {
					t.Fatal(err)
				}
			}
			flags, err := unix.FcntlInt(life.Fd(), unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, path, kittyOutputHelperArgument)
			command.ExtraFiles = []*os.File{life, socket}
			if err := command.Run(); err == nil || ctx.Err() != nil {
				t.Fatalf("unconnected control accepted or hung: %v, %v", err, ctx.Err())
			}
			after, err := unix.FcntlInt(life.Fd(), unix.F_GETFL, 0)
			if err != nil || flags != after {
				t.Fatalf("rejected invocation changed pipe flags: %v, %v, %v", flags, after, err)
			}
		})
	}
}
