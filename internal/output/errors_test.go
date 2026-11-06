package output

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrokenPipeDoesNotHideFailureOrCancellation(t *testing.T) {
	pipe := &WriteError{Err: errors.New("write: broken pipe")}
	require.True(t, IsBrokenPipe(pipe))
	require.True(t, IsBrokenPipe(fmt.Errorf("wrapped: %w", pipe)))
	require.True(t, IsBrokenPipe(errors.Join(pipe, pipe)))
	diagnostic := &DiagnosticError{Err: pipe}
	require.ErrorIs(t, diagnostic, pipe)
	require.False(t, IsBrokenPipe(diagnostic))
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("the streamed response failed"), errors.New("upstream broken pipe"), diagnostic} {
		require.False(t, IsBrokenPipe(errors.Join(pipe, cause)))
		require.False(t, IsBrokenPipe(fmt.Errorf("wrapped: %w", errors.Join(pipe, cause))))
	}
}
