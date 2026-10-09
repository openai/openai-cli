package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// These tests change process stderr, so they must remain sequential.
func captureInvocationStderr(t *testing.T, run func(*os.File) error) (string, error) {
	t.Helper()
	name := filepath.Join(t.TempDir(), "stderr")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = file
	closed := false
	defer func() {
		os.Stderr = previous
		if !closed {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	runErr := run(file)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

func invocationCommand(commands ...*cli.Command) *cli.Command {
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "text"},
			&cli.StringFlag{Name: "transform-error"},
		}, Commands: commands,
	}
	configureOutputPolicy(root)
	ConfigureCommandErrors(root)
	return root
}

func checkInvocationReport(t *testing.T, data, command, result string) time.Duration {
	t.Helper()
	lines := strings.Split(data, "\n")
	if len(lines) != 5 || lines[0] != "Command: "+command || lines[1] != "Format option: auto" ||
		lines[3] != "Command result: "+result || lines[4] != "" {
		t.Fatalf("unexpected invocation report: %q", data)
	}
	value, ok := strings.CutPrefix(lines[2], "Elapsed: ")
	elapsed, err := time.ParseDuration(value)
	if !ok || err != nil || elapsed < 0 || elapsed%time.Millisecond != 0 {
		t.Fatalf("invalid elapsed value: %q", lines[2])
	}
	return elapsed
}

func TestOutputInvocationReportsAfterFinalOutcome(t *testing.T) {
	beforeErr := errors.New("synthetic Before failure")
	actionErr := cli.Exit("synthetic Action failure", 27)
	afterErr := cli.Exit("synthetic After failure", 28)
	parentErr := errors.New("synthetic parent After failure")
	cleanupErr := cli.Exit("synthetic cleanup failure", 29)
	for _, tc := range []struct {
		name                                   string
		before, action, after, parent, cleanup error
	}{
		{name: "success"},
		{name: "Before", before: beforeErr},
		{name: "Action", action: actionErr},
		{name: "After", after: afterErr},
		{name: "cleanup", cleanup: cleanupErr},
		{name: "combined", action: actionErr, after: afterErr, parent: parentErr, cleanup: cleanupErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			actions := 0
			leaf := &cli.Command{Name: "synthetic", Action: func(ctx context.Context, command *cli.Command) error {
				actions++
				if _, err := io.WriteString(command.Root().Writer, "selected\x00\xffdata\n"); err != nil {
					return err
				}
				return tc.action
			}}
			root := invocationCommand(leaf)
			root.Writer = &stdout
			root.Before = func(ctx context.Context, _ *cli.Command) (context.Context, error) { return ctx, tc.before }
			var original error
			details, err := captureInvocationStderr(t, func(sink *os.File) error {
				leaf.After = func(context.Context, *cli.Command) error {
					info, err := sink.Stat()
					if err != nil {
						t.Fatal(err)
					}
					if info.Size() != 0 {
						t.Error("verbose report preceded After")
					}
					return tc.after
				}
				root.After = func(context.Context, *cli.Command) error { return tc.parent }
				return RunWithOutputPolicy(t.Context(), root, func(ctx context.Context) error {
					original = root.Run(ctx, []string{"openai", "--verbose", "synthetic"})
					if tc.cleanup != nil {
						original = errors.Join(original, tc.cleanup)
					}
					return original
				})
			})
			wantResult := "completed"
			if original != nil {
				wantResult = "failed"
				if !errors.Is(err, original) || err.Error() != original.Error() {
					t.Fatalf("final error identity or text changed: original=%v; got=%v", original, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			checkInvocationReport(t, details, "synthetic", wantResult)
			for _, cause := range []error{tc.before, tc.action, tc.after, tc.parent, tc.cleanup} {
				if cause != nil && !errors.Is(err, cause) {
					t.Errorf("lost cause %v: %v", cause, err)
				}
			}
			if tc.action != nil {
				var exit cli.ExitCoder
				if !errors.As(err, &exit) || exit.ExitCode() != 27 {
					t.Fatalf("Action exit lost precedence: %v", err)
				}
			}
			if tc.before != nil {
				if actions != 0 || stdout.Len() != 0 {
					t.Fatalf("Before failure ran Action: calls=%d bytes=%q", actions, stdout.String())
				}
			} else if actions != 1 || stdout.String() != "selected\x00\xffdata\n" {
				t.Fatalf("Action bytes or count changed: calls=%d bytes=%q", actions, stdout.String())
			}
		})
	}
}

func TestOutputInvocationRetainsFirstActionAndResetsBetweenRuns(t *testing.T) {
	calls := 0
	inner := &cli.Command{Name: "inner", Action: func(context.Context, *cli.Command) error { calls++; return nil }}
	outer := &cli.Command{Name: "outer", Action: func(ctx context.Context, _ *cli.Command) error {
		return errors.Join(inner.Action(ctx, inner), inner.Action(ctx, inner))
	}}
	root := invocationCommand(outer, inner)
	for _, name := range []string{"outer", "inner", "outer"} {
		details, err := captureInvocationStderr(t, func(*os.File) error {
			return RunWithOutputPolicy(t.Context(), root, func(ctx context.Context) error {
				return root.Run(ctx, []string{"openai", "--verbose", name})
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		checkInvocationReport(t, details, name, "completed")
	}
	if calls != 5 {
		t.Fatalf("nested actions reran or disappeared: %d", calls)
	}
}

func TestOutputInvocationPreservesDirectActionBoundary(t *testing.T) {
	root := invocationCommand(&cli.Command{Name: "synthetic", Action: func(context.Context, *cli.Command) error { return nil }})
	cause := errors.New("synthetic After failure")
	details, err := captureInvocationStderr(t, func(sink *os.File) error {
		root.After = func(context.Context, *cli.Command) error {
			info, err := sink.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() == 0 {
				t.Error("direct Command.Run lost action-scoped reporting")
			}
			return cause
		}
		return root.Run(t.Context(), []string{"openai", "--verbose", "synthetic"})
	})
	if !errors.Is(err, cause) {
		t.Fatalf("direct After error changed: %v", err)
	}
	checkInvocationReport(t, details, "synthetic", "completed")
}

func TestOutputInvocationElapsedIncludesBeforeAfterAndCleanup(t *testing.T) {
	root := invocationCommand(&cli.Command{Name: "synthetic", Action: func(context.Context, *cli.Command) error { return nil }})
	root.Before = func(ctx context.Context, _ *cli.Command) (context.Context, error) {
		time.Sleep(25 * time.Millisecond)
		return ctx, nil
	}
	root.After = func(context.Context, *cli.Command) error { time.Sleep(25 * time.Millisecond); return nil }
	details, err := captureInvocationStderr(t, func(*os.File) error {
		return RunWithOutputPolicy(t.Context(), root, func(ctx context.Context) error {
			err := root.Run(ctx, []string{"openai", "--verbose", "synthetic"})
			time.Sleep(25 * time.Millisecond)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := checkInvocationReport(t, details, "synthetic", "completed"); elapsed < 65*time.Millisecond {
		t.Fatalf("outer timing omitted lifecycle work: %s", elapsed)
	}
}

func TestOutputInvocationInterruptionsAndDiagnosticFailures(t *testing.T) {
	closed, err := os.CreateTemp(t.TempDir(), "closed-stderr")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = closed
	defer func() { os.Stderr = previous }()
	for _, tc := range []struct {
		name      string
		cause     error
		code      int
		cancel    bool
		wantWrite bool
	}{
		{"cancellation", context.Canceled, 27, false, false},
		{"deadline", context.DeadlineExceeded, 27, false, false},
		{"timeout exit", errors.New("synthetic"), 124, false, false},
		{"hangup exit", errors.New("synthetic"), 129, false, false},
		{"interrupt exit", errors.New("synthetic"), 130, false, false},
		{"termination exit", errors.New("synthetic"), 143, false, false},
		{"parent cancellation", errors.New("synthetic"), 27, true, false},
		{"ordinary failure", errors.New("synthetic"), 27, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := &interruptedOutputTestError{cause: tc.cause, code: tc.code}
			after := errors.New("synthetic After failure")
			cleanup := cli.Exit("synthetic cleanup failure", 29)
			root := invocationCommand(&cli.Command{Name: "synthetic", Action: func(context.Context, *cli.Command) error {
				if tc.cancel {
					cancel()
				}
				return failure
			}})
			root.After = func(context.Context, *cli.Command) error { return after }
			var original error
			err := RunWithOutputPolicy(ctx, root, func(ctx context.Context) error {
				original = errors.Join(root.Run(ctx, []string{"openai", "--verbose", "synthetic"}), cleanup)
				return original
			})
			var exit cli.ExitCoder
			if !errors.Is(err, failure) || !errors.Is(err, after) || !errors.Is(err, cleanup) || !errors.Is(err, original) ||
				!errors.As(err, &exit) || exit.ExitCode() != tc.code {
				t.Fatalf("combined failure lost identity, causes, or exit ordering: %v", err)
			}
			var diagnostic *diagnosticWriteError
			if errors.Is(err, os.ErrClosed) != tc.wantWrite || errors.As(err, &diagnostic) != tc.wantWrite {
				t.Fatalf("interruption guard or diagnostic provenance changed: %v", err)
			}
		})
	}
}
