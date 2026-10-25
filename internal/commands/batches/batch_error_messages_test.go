package batches

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchWorkflowErrorMessagesPreserveCleanupAndPrivacy(t *testing.T) {
	primary := &WorkflowError{message: "Could not read the selected file.", cause: errors.New("synthetic-private-response")}
	cleanup := &WorkflowError{message: "Could not remove temporary records. Check temporary storage permissions.",
		cause: &os.PathError{Op: "remove", Path: "/synthetic-private-directory/customer-data", Err: os.ErrPermission}}
	combined := errors.Join(primary, cleanup)
	for _, failure := range []error{combined, fmt.Errorf("synthetic-private-wrapper: %w", combined),
		&WorkflowError{message: "Reading records stopped.", cause: errors.Join(context.Canceled, combined)}} {
		message := WorkflowErrorMessage(failure)
		require.Contains(t, message, primary.message)
		require.Contains(t, message, cleanup.message)
		require.NotContains(t, message, "synthetic-private")
		require.NotContains(t, message, "customer-data")
		require.ErrorIs(t, failure, os.ErrPermission)
	}
	require.Equal(t, primary.message+"\n"+cleanup.message, WorkflowErrorMessage(errors.Join(combined, cleanup)))
	require.Equal(t, primary.message, WorkflowErrorMessage(&WorkflowError{message: primary.message, cause: primary}))
	require.Empty(t, WorkflowErrorMessage(errors.New("synthetic-private-response")))
}
