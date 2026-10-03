package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type failedImagePickerWriter struct{}

func (failedImagePickerWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestImagePickerSelectionEchoFailurePreventsGeneration(t *testing.T) {
	m, err := newImagePicker(imagePickerOptions{Prompt: "Synthetic prompt"})
	require.NoError(t, err)
	result := imagePickerResult{Args: m.settings.args(), settings: m.settings, shell: "bash"}
	err = finishImagePickerSelection(context.Background(), failedImagePickerWriter{}, result)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	var output bytes.Buffer
	require.NoError(t, finishImagePickerSelection(context.Background(), &output, result))
	require.Equal(t, formatImagePickerCommand(result.Args, "bash")+"\n", output.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output.Reset()
	require.True(t, errors.Is(finishImagePickerSelection(ctx, &output, result), context.Canceled))
	require.Empty(t, output.String())
}

func TestImagePickerUnsupportedShellSelection(t *testing.T) {
	for _, shell := range []string{"", "sh", "powershell.exe", "unknown"} {
		t.Run(shell, func(t *testing.T) {
			m := pickerForTest(t)
			result := imagePickerResult{Args: m.settings.args(), settings: m.settings, shell: shell}
			var output bytes.Buffer
			require.NoError(t, finishImagePickerSelection(context.Background(), &output, result))
			require.Empty(t, output.String(), "generation must not echo a command for the wrong shell")
			result.PrintOnly = true
			require.ErrorContains(t, finishImagePickerSelection(context.Background(), &output, result), imagePickerUnsupportedShell)
			require.Empty(t, output.String())
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, finishImagePickerSelection(ctx, &output, result), context.Canceled)
		})
	}
}
