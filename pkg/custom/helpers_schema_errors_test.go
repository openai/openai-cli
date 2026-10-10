package custom

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func schemaErrorTestCommand(t *testing.T, flags ...string) *cli.Command {
	t.Helper()
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&cli.StringFlag{Name: "format-error", Value: "text"}, &cli.StringFlag{Name: "format"},
		&cli.StringFlag{Name: "transform-error"}, &cli.BoolFlag{Name: "raw-output"},
	}, Action: func(context.Context, *cli.Command) error { return nil }}
	require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, flags...)))
	return root
}

func TestHelperSchemaCleanupErrorModes(t *testing.T) {
	apierr := &openai.Error{StatusCode: 500}
	require.NoError(t, json.Unmarshal([]byte(`{"message":"synthetic failure","code":"fixture_code"}`), apierr))
	apierr.StatusCode = 500
	cleanup := &schemaHelperError{message: "Could not clean up the schema staging file. Inspect the output directory."}
	combined := &schemaHelperCleanupError{operation: apierr, cleanup: cleanup}
	for _, tc := range []struct {
		format, transform string
		envelope          bool
	}{
		{"json", "", true}, {"yaml", "", true}, {"raw", "", false}, {"json", "code", false},
	} {
		t.Run(tc.format+tc.transform, func(t *testing.T) {
			root := schemaErrorTestCommand(t, "--format-error", tc.format, "--transform-error", tc.transform)
			var output strings.Builder
			require.NoError(t, ShowCommandError(root, combined, &output))
			if tc.envelope {
				require.Contains(t, output.String(), "api_error")
				require.Contains(t, output.String(), "cleanup_error")
			} else {
				var ordinary strings.Builder
				require.NoError(t, ShowCommandError(root, apierr, &ordinary))
				require.Equal(t, ordinary.String(), output.String())
			}
		})
	}
	root := schemaErrorTestCommand(t)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		var output strings.Builder
		require.NoError(t, ShowCommandError(root, errors.Join(combined, cause), &output))
		require.Contains(t, output.String(), cleanup.message)
		require.NotContains(t, output.String(), "API could not complete")
		if cause == context.Canceled {
			require.Contains(t, output.String(), "canceled")
		} else {
			require.Contains(t, output.String(), "timed out")
		}
	}
}

func TestHelperSchemaSavedErrorWholeCause(t *testing.T) {
	root := schemaErrorTestCommand(t)
	for _, tc := range []struct {
		cause   error
		message string
		code    int
	}{
		{errors.New("receipt sink failed"), "receipt failed", 1},
		{context.Canceled, "canceled", 130},
		{context.DeadlineExceeded, "timed out", 1},
		{&downloadSignalError{exitCode: 143}, "canceled", 143},
	} {
		failure := &schemaHelperError{message: "Schema saved; receipt failed.", cause: tc.cause, saved: true}
		wrapped := errors.Join(errors.New("other failure"), failure)
		var output strings.Builder
		require.NoError(t, ShowCommandError(root, wrapped, &output))
		require.Contains(t, output.String(), "Schema saved")
		require.Contains(t, output.String(), tc.message)
		require.Equal(t, tc.code, failure.ExitCode())
		require.ErrorIs(t, wrapped, tc.cause)
	}
}
