package batches

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

func TestBatchWorkflowErrorMessagesKeepCompleteRecoveryOnce(t *testing.T) {
	interrupted := &WorkflowError{message: "Batch browser interrupted. Existing batches keep running.", code: 130, cause: context.Canceled}
	recovery := &WorkflowError{message: "Batch creation is unconfirmed. Check the batch list before creating another batch.\n" + interrupted.message, cause: interrupted}
	cleanup := &WorkflowError{message: "Could not remove temporary records. Check temporary storage permissions.",
		cause: &os.PathError{Op: "remove", Path: "/synthetic-private-directory/customer-data", Err: os.ErrPermission}}
	for _, failure := range []error{
		errors.Join(interrupted, recovery, cleanup),
		errors.Join(cleanup, interrupted, recovery),
		errors.Join(recovery, cleanup, interrupted),
	} {
		message := WorkflowErrorMessage(failure)
		require.Equal(t, 1, strings.Count(message, interrupted.message))
		require.Contains(t, message, recovery.message)
		require.Contains(t, message, cleanup.message)
		require.NotContains(t, message, "synthetic-private")
		require.NotContains(t, message, "customer-data")
		require.ErrorIs(t, failure, context.Canceled)
		require.ErrorIs(t, failure, os.ErrPermission)
	}
}
