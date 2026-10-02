package custom

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Persistence belongs to the interactive session, never direct API commands.
func runImagePickerSession(ctx context.Context, input, output *os.File, options imagePickerOptions) (imagePickerResult, error) {
	if options.Shell == "" {
		options.Shell = os.Getenv("OPENAI_PICKER_SHELL")
	}
	if options.Shell == "" {
		// Enter launches have no hook-provided shell. Match the immediate
		// caller so the displayed command can be pasted back into that shell.
		options.Shell = imagePickerParentShell(ctx)
	}
	if ctx.Err() != nil || input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return runImagePicker(ctx, input, output, options)
	}
	path, stateErr := imagePickerStatePath()
	if stateErr == nil {
		var settings imagePickerSettings
		var found bool
		settings, found, stateErr = loadImagePickerState(ctx, path)
		if stateErr == nil && found && options.initial == nil {
			options.initial = &settings
		}
	}
	if err := ctx.Err(); err != nil {
		return imagePickerResult{}, err
	}
	if stateErr != nil {
		options.initialNote = "Could not restore saved settings. Using defaults; your saved settings are kept."
		if options.initial != nil {
			options.initialNote = "Could not read saved settings. Keeping this session's choices."
		}
	}
	result, err := runImagePicker(ctx, input, output, options)
	if err != nil || result.Canceled {
		return result, err
	}
	// Unknown, corrupt, or inaccessible state remains untouched this session.
	if stateErr != nil {
		path = ""
	}
	return result, finishImagePickerSelection(ctx, output, os.Stderr, path, result)
}

// Echo before saving history or starting a request. Recording at submission
// means a slower API response cannot overwrite a newer interactive selection.
func finishImagePickerSelection(ctx context.Context, output, diagnostics io.Writer, path string, result imagePickerResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.Canceled || len(result.Args) < 4 || (len(result.Args)-2)%2 != 0 {
		return fmt.Errorf("no image request was selected")
	}
	command := formatImagePickerCommand(result.Args, result.shell)
	if command == "" {
		if result.PrintOnly {
			return imageSavingFailure(imagePickerUnsupportedShell, nil)
		}
		return nil
	}
	if _, err := fmt.Fprintln(output, command); err != nil {
		return imageSavingFailure("Could not print the command. No image request was started.", err)
	}
	if path == "" {
		return nil
	}
	if err := saveImagePickerState(ctx, path, result.settings); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		// Storage is optional. Do not expose stored prompts or raw filesystem
		// errors, and do not prevent an otherwise valid image request.
		if _, err := fmt.Fprintln(diagnostics, "Could not remember these settings."); err != nil {
			return imageSavingFailure("Could not print the settings warning. No image request was started.", err)
		}
	}
	return nil
}
