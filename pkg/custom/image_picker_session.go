package custom

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Keep choices within the current invocation. Direct API commands never enter
// this session or acquire picker state.
func runImagePickerSession(ctx context.Context, input, output *os.File, options imagePickerOptions) (imagePickerResult, error) {
	if options.Shell == "" {
		options.Shell = os.Getenv("OPENAI_PICKER_SHELL")
	}
	if options.Shell == "" {
		options.Shell = imagePickerParentShell(ctx)
	}
	result, err := runImagePicker(ctx, input, output, options)
	if err != nil || result.Canceled {
		return result, err
	}
	return result, finishImagePickerSelection(ctx, output, result)
}

// Echo the selected command only after restoring the terminal and before
// starting the request. A failed echo must not silently start generation.
func finishImagePickerSelection(ctx context.Context, output io.Writer, result imagePickerResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.Canceled || len(result.Args) < 4 || (len(result.Args)-2)%2 != 0 {
		return fmt.Errorf("no image request was selected")
	}
	if _, err := fmt.Fprintln(output, formatImagePickerCommand(result.Args, result.shell)); err != nil {
		return imageSavingFailure("Could not print the command. No image request was started.", err)
	}
	return nil
}
