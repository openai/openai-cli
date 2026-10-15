package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

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
	for _, independent := range []error{
		errors.New("independent diagnostic failure"),
		&os.PathError{Op: "write", Path: "independent", Err: os.ErrClosed},
	} {
		for _, joined := range []bool{false, true} {
			err := withBatchErrorOutput(batchContextError(context.DeadlineExceeded), io.Discard,
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
}

func TestBatchCancellationDiagnosticsBoundOnlyTheirOwnDelivery(t *testing.T) {
	err := withBatchErrorOutput(batchContextError(context.DeadlineExceeded), io.Discard,
		func(ctx context.Context, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		})
	if err != nil {
		t.Fatalf("owned delivery deadline would trigger another diagnostic: %v", err)
	}
	independent := errors.New("ordinary diagnostic failure")
	err = withBatchErrorOutput(independent, io.Discard, func(ctx context.Context, _ io.Writer) error {
		if _, bounded := ctx.Deadline(); bounded {
			t.Fatal("ordinary error acquired the batch cleanup deadline")
		}
		return independent
	})
	if !errors.Is(err, independent) {
		t.Fatalf("ordinary error changed: %v", err)
	}
}
