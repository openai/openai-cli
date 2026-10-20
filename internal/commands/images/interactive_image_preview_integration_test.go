package images_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestInteractiveImageFailureKeepsExitCodeAndViewerDiagnostic(t *testing.T) {
	for _, code := range []int{0, 130, 143, 129} {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(images.InteractiveExitForTest(code))
		err := images.TestFinishInteractiveImagePreview(ctx, nil)
		if code == 0 {
			require.NoError(t, err)
		} else {
			var exit cli.ExitCoder
			require.ErrorAs(t, err, &exit)
			require.Equal(t, code, exit.ExitCode())
			require.Empty(t, exit.Error())
		}
		failure := errors.Join(context.DeadlineExceeded, io.ErrClosedPipe)
		err = images.TestFinishInteractiveImagePreview(ctx, failure)
		var exit cli.ExitCoder
		require.ErrorAs(t, err, &exit)
		require.Equal(t, max(1, code), exit.ExitCode())
		require.ErrorIs(t, err, failure)
		var output bytes.Buffer
		require.NoError(t, custom.ShowCommandError(&cli.Command{}, err, &output))
		require.Contains(t, output.String(), "interactive preview")
		require.Contains(t, output.String(), "original file is unchanged")
		require.NotContains(t, output.String(), "API")
		require.NotContains(t, output.String(), "permissions")
	}
}
