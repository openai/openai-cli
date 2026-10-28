package images_test

import (
	"errors"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/stretchr/testify/require"
)

func TestSavedImageOutputFailureRetainsFilesAndPartialError(t *testing.T) {
	plan := images.NewOutputPlanForTest(t.TempDir(), "robot", "")
	sinkErr := errors.New("closed synthetic sink")
	err := plan.SaveForTest(t.Context(), []byte(`{"data":[{"b64_json":"iVBORw0KGgo="},{"b64_json":"invalid"}]}`), errorSink{sinkErr})
	require.ErrorIs(t, err, sinkErr)
	require.Contains(t, err.Error(), "could not save the entire response")
	require.Contains(t, err.Error(), "print all saved paths")
	message := imageErrorMessage(nil, err)
	require.Contains(t, message, "could not save the entire response")
	require.Contains(t, message, "print all saved paths")
	require.Contains(t, message, "Check the output folder before generating again")
	require.NotContains(t, message, "listed files")
	files, readErr := os.ReadDir(plan.DirectoryForTest())
	require.NoError(t, readErr)
	require.Len(t, files, 1)
}
