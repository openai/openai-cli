package batches

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestBatchBrowserWorkPreservesInterruptedCleanup(t *testing.T) {
	interrupted := &WorkflowError{message: "Batch browser interrupted. Existing batches keep running.", code: 130, cause: context.Canceled}
	cleanup := &WorkflowError{message: "Could not remove all temporary batch records. Check temporary storage permissions before retrying.",
		cause: &os.PathError{Op: "remove", Path: "/synthetic-private/snapshot", Err: os.ErrPermission}}
	result, err := batchBrowserWork(t.Context(), "Preparing input file", func(context.Context, *batchBrowserScreen) error {
		return interrupted
	}, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "retained worker result", errors.Join(ctx.Err(), cleanup)
	})
	require.Equal(t, "retained worker result", result)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, os.ErrPermission)
	var exit cli.ExitCoder
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 130, exit.ExitCode())
	message := WorkflowErrorMessage(err)
	require.Contains(t, message, cleanup.message)
	require.Equal(t, 1, strings.Count(message, interrupted.message))
	require.NotContains(t, message, "synthetic-private")
}
