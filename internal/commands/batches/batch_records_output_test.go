package batches

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBatchResultsErrorDrainPreservesOutputAndCause(t *testing.T) {
	for _, scenario := range []string{"drain", "write-error", "cancel", "custom"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			payload := bytes.Repeat([]byte("synthetic result\n"), 32*1024)
			var output bytes.Buffer
			var destination io.Writer = &output
			finish := func() {}
			if scenario != "custom" {
				reader, writer, err := os.Pipe()
				require.NoError(t, err)
				defer reader.Close()
				defer writer.Close()
				destination = writer
				if scenario == "write-error" {
					require.NoError(t, reader.Close())
					payload = []byte("synthetic result\n")
				} else {
					done := make(chan error, 1)
					go func() { _, err := io.Copy(&output, reader); done <- err }()
					finish = func() { require.NoError(t, writer.Close()); require.NoError(t, <-done) }
				}
			}
			cause := errors.New("synthetic record failure")
			failure := &WorkflowError{message: "synthetic record failure", code: 7, cause: cause}
			err := writeBatchStdout(ctx, destination, func(out io.Writer) error {
				n, writeErr := out.Write(payload)
				require.NoError(t, writeErr)
				require.Equal(t, len(payload), n)
				if scenario == "cancel" {
					cancel()
				}
				return &batchOutputDrainError{failure}
			})
			finish()
			require.ErrorIs(t, err, cause)
			if scenario == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, ctx.Err())
				require.NotErrorIs(t, err, context.Canceled)
				var exit interface{ ExitCode() int }
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 7, exit.ExitCode())
				if scenario == "write-error" {
					require.Contains(t, WorkflowErrorMessage(err), "output destination")
				} else {
					require.Equal(t, payload, output.Bytes())
				}
			}
		})
	}
}
