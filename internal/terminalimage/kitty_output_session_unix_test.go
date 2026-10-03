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
	"strings"
	"syscall"
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

func TestKittySessionReportsReserveFailureOnlyWhenNeeded(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, needed := range []bool{false, true} {
		t.Run(fmt.Sprintf("needed=%t", needed), func(t *testing.T) {
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
			if err := session.workers[1].command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			const image = "complete image"
			_, err = session.write(t.Context(), func(w io.Writer) error {
				_, err := io.WriteString(w, image)
				return err
			})
			if err != nil {
				t.Fatalf("primary output failed: %v", err)
			}
			if needed {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				_, err := session.write(ctx, func(io.Writer) error { cancel(); return nil })
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("primary cancellation lost: %v", err)
				}
				_, err = session.write(t.Context(), func(io.Writer) error {
					t.Error("failed reserve accepted output")
					return nil
				})
				if err == nil {
					t.Fatal("required reserve failure was hidden")
				}
			}
			if err := session.Close(); err != nil {
				t.Fatalf("cleanup reported an unused or already reported failure: %v", err)
			}
			data, err := os.ReadFile(out.Name())
			if err != nil || string(data) != image {
				t.Fatalf("primary output changed: %q, %v", data, err)
			}
			for _, worker := range session.workers {
				if worker.command.ProcessState == nil {
					t.Fatal("worker was not reaped")
				}
			}
		})
	}
}

func TestKittyWorkerFailureReapsBeforeReturning(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"killed", "stopped-then-canceled", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			outRead, outWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer outRead.Close()
			defer outWrite.Close()
			session, err := startKittySession(t.Context(), path, outWrite)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := session.write(ctx, func(out io.Writer) error {
					_, err := io.Copy(out, strings.NewReader(strings.Repeat("image", 1<<20)))
					return err
				})
				done <- err
			}()
			if err := outRead.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var first [1]byte
			if _, err := outRead.Read(first[:]); err != nil {
				t.Fatal(err)
			}
			worker := session.workers[0]
			sig := syscall.SIGKILL
			if mode == "stopped-then-canceled" {
				sig = syscall.SIGSTOP
			} else if mode == "interrupt" {
				sig = syscall.SIGINT
			}
			if err := worker.command.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			if mode == "stopped-then-canceled" {
				cancel()
			}
			// Never drain output to make cancellation or helper failure finish.
			select {
			case err := <-done:
				if err == nil || (mode != "killed" && !errors.Is(err, context.Canceled)) {
					t.Fatalf("worker failure lost: %v", err)
				}
			case <-time.After(3 * time.Second):
				outRead.Close()
				t.Fatal("worker failure left producer blocked")
			}
			if worker.command.ProcessState == nil {
				t.Fatal("worker was not reaped before write returned")
			}
			if !worker.retired.Load() || session.workers[1].retired.Load() {
				t.Fatal("failure consumed the idle reset worker")
			}
		})
	}
}

func TestKittyCloseReapsStoppedIdleWorkers(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	session, err := startKittySession(t.Context(), path, out)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.write(t.Context(), func(w io.Writer) error {
		_, err := io.WriteString(w, "image")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, worker := range session.workers {
		if err := worker.command.Process.Signal(syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	if err := session.Close(); err == nil {
		t.Fatal("forced shutdown failure was not reported")
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("stopped idle workers outlived shutdown: %v", elapsed)
	}
	for _, worker := range session.workers {
		if worker.command.ProcessState == nil {
			t.Fatal("idle worker was not reaped")
		}
	}
}

func TestKittyJobRejectsWritePipeBeforeReadiness(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	worker, err := startKittyWorker(t.Context(), path, out)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	flags, err := unix.FcntlInt(w.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := worker.control.WriteMsgUnix([]byte{'J'}, unix.UnixRights(int(w.Fd())), nil); err != nil {
		t.Fatal(err)
	}
	status, fds, err := readKittyControl(worker.control)
	closeKittyDescriptors(fds)
	if err != nil || status != '1' || len(fds) != 0 {
		t.Fatalf("invalid job entered output phase: status=%q, err=%v", status, err)
	}
	after, err := unix.FcntlInt(w.Fd(), unix.F_GETFL, 0)
	if err != nil || flags != after {
		t.Fatalf("rejected pipe flags changed: %v, %v, %v", flags, after, err)
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
