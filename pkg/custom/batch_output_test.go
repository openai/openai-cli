package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

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
