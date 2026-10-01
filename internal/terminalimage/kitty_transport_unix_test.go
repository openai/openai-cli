//go:build darwin || linux

package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// One-shot session wrapper for focused native transport tests.
func runKittyWriter(ctx context.Context, path string, out *os.File, write func(io.Writer) error) (written bool, err error) {
	session, err := startKittySession(ctx, path, out)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	return session.write(ctx, write)
}

func TestKittyCatPreservesBytesAndWaits(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	payload := strings.Repeat("\x1b_Gq=2,m=1;YWJj\x1b\\", 8192) + "\x1b_Gq=2,m=0;\x1b\\"
	written, err := runKittyWriter(context.Background(), path, out, func(destination io.Writer) error {
		_, err := io.WriteString(destination, payload)
		return err
	})
	if err != nil || !written {
		t.Fatalf("native output failed: written=%v, err=%v", written, err)
	}
	got, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("native output was incomplete when call returned: got %d, want %d bytes", len(got), len(payload))
	}
}

func TestKittyCatRetainsFailures(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	t.Run("spawn", func(t *testing.T) {
		called := false
		written, err := runKittyWriter(context.Background(), filepath.Join(t.TempDir(), "missing-cat"), out, func(io.Writer) error {
			called = true
			return nil
		})
		if called || written || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("startup failure wrote data or lost cause: called=%v, written=%v, err=%v", called, written, err)
		}
	})
	t.Run("writer", func(t *testing.T) {
		cause := errors.New("synthetic encoder failure")
		written, err := runKittyWriter(context.Background(), path, out, func(destination io.Writer) error {
			_, err := io.WriteString(destination, "partial")
			return errors.Join(cause, err)
		})
		if !written || !errors.Is(err, cause) {
			t.Fatalf("writer failure lost cause: written=%v, err=%v", written, err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		written, err := runKittyWriter(ctx, path, out, func(destination io.Writer) error {
			_, err := io.WriteString(destination, "partial")
			cancel()
			return err
		})
		if !written || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost after process wait: written=%v, err=%v", written, err)
		}
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			t.Fatalf("helper exit status escaped into CLI error: %v", err)
		}
	})
}

func TestKittyOutputNonTerminalPreservesWriter(t *testing.T) {
	var out bytes.Buffer
	cause := errors.New("synthetic output failure")
	err := writeKittyOutput(context.Background(), &out, func(destination io.Writer) error {
		if destination != &out {
			t.Fatal("non-terminal output was wrapped")
		}
		_, err := io.WriteString(destination, "unchanged")
		return errors.Join(cause, err)
	})
	if out.String() != "unchanged" || !errors.Is(err, cause) {
		t.Fatalf("non-terminal output changed: %q, %v", out.String(), err)
	}
}

func TestKittyCatOutputFailureDoesNotSetCLIExitCode(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A read-only descriptor makes the native helper fail its own output write.
	out, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	_, err = runKittyWriter(context.Background(), path, out, func(destination io.Writer) error {
		_, err := io.WriteString(destination, "payload")
		return err
	})
	var exit interface{ ExitCode() int }
	if err == nil || errors.As(err, &exit) || !strings.Contains(err.Error(), "write image to terminal") {
		t.Fatalf("helper output failure lost or exposed as CLI exit code: %v", err)
	}
}

func TestKittyWriterPanicReapsSupervisor(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	cause := errors.New("synthetic writer panic")
	defer func() {
		if got := recover(); got != cause {
			t.Errorf("writer panic was replaced: %v", got)
		}
	}()
	_, _ = runKittyWriter(t.Context(), path, out, func(io.Writer) error {
		panic(cause)
	})
}

func TestKittySupervisorLifelineStopsBlockedWriter(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
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
	writes := make(chan error, 1)
	go func() {
		_, err := session.write(t.Context(), func(out io.Writer) error {
			_, err := io.Copy(out, strings.NewReader(strings.Repeat("image bytes", 1<<18)))
			return err
		})
		writes <- err
	}()
	if err := outRead.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var first [1]byte
	if _, err := outRead.Read(first[:]); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("session closure failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		outRead.Close()
		t.Fatal("supervisor did not reap blocked native writer")
	}
	select {
	case err := <-writes:
		if err == nil {
			t.Fatal("blocked producer unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native writer retained data pipe after closure")
	}
}

func TestKittyOutputHelperRejectsMissingPipes(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(path, kittyOutputHelperArgument)
	if err := command.Run(); err == nil {
		t.Fatal("private helper accepted an invocation without its pipe descriptors")
	}
	if handled, err := RunKittyOutputHelper([]string{"openai", kittyOutputHelperArgument, "extra"}); !handled || err == nil {
		t.Fatalf("private helper accepted extra arguments: handled=%v, err=%v", handled, err)
	}
}

func TestKittyOutputHelperRejectsInvalidLifeline(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"regular-file", "write-end", "closed-parent"} {
		t.Run(scenario, func(t *testing.T) {
			dataRead, dataWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer dataRead.Close()
			defer dataWrite.Close()
			lifeRead, lifeWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer lifeRead.Close()
			defer lifeWrite.Close()
			life := lifeRead
			switch scenario {
			case "regular-file":
				life, err = os.Create(filepath.Join(t.TempDir(), "not-a-lifeline"))
				if err != nil {
					t.Fatal(err)
				}
				defer life.Close()
			case "write-end":
				life = lifeWrite
			case "closed-parent":
				_ = lifeWrite.Close()
			}
			flags, err := unix.FcntlInt(life.Fd(), unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, path, kittyOutputHelperArgument)
			command.Stdin = dataRead
			command.ExtraFiles = []*os.File{life}
			out, err := command.Output()
			if err == nil || ctx.Err() != nil || len(out) != 0 {
				t.Fatalf("invalid lifeline emitted data or hung: output=%q, err=%v, context=%v", out, err, ctx.Err())
			}
			if scenario != "closed-parent" {
				after, err := unix.FcntlInt(life.Fd(), unix.F_GETFL, 0)
				if err != nil || after != flags {
					t.Fatalf("invalid lifeline flags changed: before=%v, after=%v, err=%v", flags, after, err)
				}
			}
		})
	}
}
