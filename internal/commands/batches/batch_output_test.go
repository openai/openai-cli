package batches

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

const batchOutputLifecycleFixtureArgument = "__test-batch-output-lifecycle"

func TestBatchOutputPipeFailureGuidance(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		name := "destination-failure"
		if stopped {
			name = "owned-cancellation"
		}
		t.Run(name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			var state atomic.Bool
			state.Store(stopped)
			if stopped {
				err = writer.Close()
			} else {
				err = reader.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = (batchPipeWriter{file: writer, stopped: &state}).Write([]byte("synthetic output"))
			if err == nil {
				t.Fatal("closed pipe write became success")
			}
			var pathError *os.PathError
			if !errors.As(err, &pathError) {
				t.Fatalf("write cause was lost: %v", err)
			}
			message := WorkflowErrorMessage(err)
			var owned *batchOwnedPipeClosed
			if stopped {
				if !errors.As(err, &owned) || message != "" {
					t.Fatalf("owned cancellation became an output failure: %v", err)
				}
			} else if !strings.Contains(message, "receiving command or output destination") || strings.Contains(message, "permissions") {
				t.Fatalf("incorrect output recovery guidance: %q", message)
			}
		})
	}
}

// The fixture observes the real helper before its process exits. Normal return
// must not close the handle still used by its parent-death watcher.
func runBatchOutputLifecycleFixture(args []string) (bool, int) {
	if len(args) < 2 || args[1] != batchOutputLifecycleFixtureArgument {
		return false, 0
	}
	if len(args) != 4 {
		return true, 2
	}
	descriptor, err := strconv.ParseUint(args[3], 10, strconv.IntSize)
	if err != nil {
		return true, 2
	}
	wantError, wantOpen := false, true
	switch args[2] {
	case "drain":
	case "write-error", "ready-error":
		wantError = true
	case "invalid-lifeline":
		wantError, wantOpen = true, false
	default:
		return true, 2
	}
	handled, err := RunBatchOutputHelper([]string{args[0], batchOutputHelperArgument, args[3]})
	if !handled || (err != nil) != wantError {
		return true, 3
	}
	if batchOutputFixtureHandleOpen(uintptr(descriptor)) != wantOpen {
		return true, 4
	}
	marker := os.Stderr
	if args[2] == "ready-error" {
		marker = os.Stdout
	}
	if _, err := io.WriteString(marker, "returned\n"); err != nil {
		return true, 5
	}
	if wantError {
		return true, 1
	}
	return true, 0
}

func TestBatchOutputHelperLifelineOwnership(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"drain", "write-error", "ready-error", "invalid-lifeline"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			life, parent, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer life.Close()
			defer parent.Close()
			if scenario == "invalid-lifeline" {
				life, err = os.CreateTemp(t.TempDir(), "invalid-lifeline-")
				if err != nil {
					t.Fatal(err)
				}
				defer life.Close()
			}
			command := exec.CommandContext(ctx, binary, batchOutputLifecycleFixtureArgument, scenario)
			command.Env = []string{"GOMAXPROCS=2"}
			command.WaitDelay = 2 * time.Second
			if err := inheritBatchOutputLifeline(command, life); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			var input []byte
			wantCode, wantReady := 1, "Rreturned\n"
			switch scenario {
			case "drain":
				// Exceed pipe capacity so success requires complete output draining.
				input = bytes.Repeat([]byte("synthetic output 資料\n"), 8192)
				wantCode = 0
			case "write-error", "ready-error":
				readOnly, err := os.Open(os.DevNull)
				if err != nil {
					t.Fatal(err)
				}
				defer readOnly.Close()
				if scenario == "write-error" {
					input = []byte("synthetic output\n")
					command.Stdout = readOnly
				} else {
					command.Stderr = readOnly
					wantReady = ""
				}
			case "invalid-lifeline":
				wantReady = "returned\n"
			}
			command.Stdin = bytes.NewReader(input)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			_ = life.Close()
			err = command.Wait()
			if ctx.Err() != nil {
				t.Fatalf("helper did not return with its parent alive: %v; stderr=%q", ctx.Err(), stderr.String())
			}
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != wantCode || stderr.String() != wantReady {
				t.Fatalf("helper code=%d stderr=%q; want code=%d stderr=%q", code, stderr.String(), wantCode, wantReady)
			}
			if scenario == "drain" && !bytes.Equal(stdout.Bytes(), input) {
				t.Fatalf("helper did not drain output: got %d bytes, want %d", stdout.Len(), len(input))
			}
			if scenario == "ready-error" && stdout.String() != "returned\n" {
				t.Fatalf("readiness failure did not return: stdout=%q", stdout.String())
			}
		})
	}
}

func TestBatchOutputSmallWriteFailure(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// A small write fits in the owned data pipe before the helper rejects its
	// read-only destination. The parent's final result must retain that failure.
	const payload = "synthetic output\n"
	entered, written := false, 0
	var callbackError error
	err = writeBatchStdout(ctx, reader, func(out io.Writer) error {
		entered = true
		written, callbackError = io.WriteString(out, payload)
		return callbackError
	})
	if ctx.Err() != nil {
		t.Fatalf("output failure did not finish: %v", ctx.Err())
	}
	if !entered || written != len(payload) || callbackError != nil {
		t.Fatalf("callback did not complete its write: entered=%v bytes=%d error=%v", entered, written, callbackError)
	}
	if err == nil {
		t.Fatal("output failure became success")
	}
}

func TestBatchInterruptedDiagnosticsFindNestedStderr(t *testing.T) {
	other, err := os.CreateTemp(t.TempDir(), "output-")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	stderr := &batchOutputInterrupted{error: context.DeadlineExceeded, output: os.Stderr}
	unrelated := &batchOutputInterrupted{error: context.DeadlineExceeded, output: other}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"secondary wrapped by stdout":   {&batchOutputInterrupted{error: fmt.Errorf("presentation: %w", stderr), output: other}, true},
		"secondary after joined output": {errors.Join(unrelated, stderr), true},
		"other destination only":        {unrelated, false},
		"deadline without delivery":     {context.DeadlineExceeded, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := interruptedBatchDiagnostic(tc.err); got != tc.want {
				t.Fatalf("interrupted stderr = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBatchCancellationDiagnosticsPreserveIndependentFailures(t *testing.T) {
	// This path uses an in-memory writer, so its delivery deadline can use virtual time.
	synctest.Test(t, func(t *testing.T) {
		for _, independent := range []error{
			errors.New("independent diagnostic failure"),
			&os.PathError{Op: "write", Path: "independent", Err: os.ErrClosed},
		} {
			for _, joined := range []bool{false, true} {
				err := WithErrorOutput(batchContextError(context.DeadlineExceeded), io.Discard,
					func(ctx context.Context, _ io.Writer) error {
						<-ctx.Done()
						if joined {
							return errors.Join(ctx.Err(), independent)
						}
						return independent
					})
				if !errors.Is(err, independent) {
					t.Fatalf("diagnostic failure was lost (joined=%v): %v", joined, err)
				}
			}
		}
	})
}

func TestBatchCancellationDiagnosticsBoundOnlyTheirOwnDelivery(t *testing.T) {
	// This path uses an in-memory writer, so its delivery deadline can use virtual time.
	synctest.Test(t, func(t *testing.T) {
		err := WithErrorOutput(batchContextError(context.DeadlineExceeded), io.Discard,
			func(ctx context.Context, _ io.Writer) error {
				<-ctx.Done()
				return ctx.Err()
			})
		if err != nil {
			t.Fatalf("owned delivery deadline would trigger another diagnostic: %v", err)
		}
		independent := errors.New("ordinary diagnostic failure")
		err = WithErrorOutput(independent, io.Discard, func(ctx context.Context, _ io.Writer) error {
			if _, bounded := ctx.Deadline(); bounded {
				t.Fatal("ordinary error acquired the batch cleanup deadline")
			}
			return independent
		})
		if !errors.Is(err, independent) {
			t.Fatalf("ordinary error changed: %v", err)
		}
	})
}
