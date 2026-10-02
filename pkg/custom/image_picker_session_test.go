package custom

import (
	"bytes"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
)

type failedImagePickerWriter struct{}

func (failedImagePickerWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestImagePickerSelectionEchoFailurePreventsGeneration(t *testing.T) {
	m, err := newImagePicker(imagePickerOptions{Prompt: "Synthetic prompt"})
	require.NoError(t, err)
	result := imagePickerResult{Args: m.settings.args(), settings: m.settings}
	err = finishImagePickerSelection(context.Background(), failedImagePickerWriter{}, result)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	var output bytes.Buffer
	require.NoError(t, finishImagePickerSelection(context.Background(), &output, result))
	require.Equal(t, formatImagePickerCommand(result.Args, "")+"\n", output.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output.Reset()
	require.True(t, errors.Is(finishImagePickerSelection(ctx, &output, result), context.Canceled))
	require.Empty(t, output.String())
}
