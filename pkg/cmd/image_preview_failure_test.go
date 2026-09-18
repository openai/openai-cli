package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportImagePreviewFailureSharpRetry(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		var previewErr error = &imageFontPreviewError{cause: errors.New("widen the terminal to at least 33 columns")}
		if wrap {
			previewErr = fmt.Errorf("optional preview: %w", previewErr)
		}
		var output bytes.Buffer
		require.NoError(t, reportImagePreviewFailure(&output, previewErr))
		require.Contains(t, output.String(), "Sharp inline preview unavailable")
		require.Contains(t, output.String(), "widen the terminal to at least 33 columns")
		require.Contains(t, output.String(), "openai images preview FILE")
		require.Contains(t, output.String(), "--open")
		require.Contains(t, output.String(), "The generated image is saved; no new generation is needed.")
	}
}

func TestReportImagePreviewFailureEscapesControls(t *testing.T) {
	cause := errors.New("bad file\n\r\t\x1b[2J\x07\"name")
	var output bytes.Buffer
	require.NoError(t, reportImagePreviewFailure(&output, &imageFontPreviewError{cause: cause}))
	text := output.String()
	require.Contains(t, text, `bad file\n\r\t\x1b[2J\a\"name`)
	for _, control := range []string{"\r", "\t", "\x1b", "\x07"} {
		require.NotContains(t, text, control)
	}
	require.Equal(t, 2, strings.Count(text, "\n"), "only the two intended output lines may contain newlines")
}

func TestReportImagePreviewFailureHidesDecoderDetails(t *testing.T) {
	var output bytes.Buffer
	err := errors.New("decoder failed at private source /synthetic/private-image.png\n\x1b[2J")
	require.NoError(t, reportImagePreviewFailure(&output, err))
	require.Equal(t, "Preview unavailable; open the saved image to view it.\n", output.String())
}

func TestReportImagePreviewFailurePropagatesWriterError(t *testing.T) {
	failure := errors.New("synthetic writer failure")
	for _, previewErr := range []error{errors.New("decoder failure"), &imageFontPreviewError{cause: errors.New("font failure")}} {
		require.ErrorIs(t, reportImagePreviewFailure(imagePreviewFailureWriter{failure}, previewErr), failure)
	}
}

type imagePreviewFailureWriter struct{ err error }

func (w imagePreviewFailureWriter) Write([]byte) (int, error) { return 0, w.err }
