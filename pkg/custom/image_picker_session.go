package custom

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
		if stateErr == nil && found {
			options = restoreImagePickerDraft(options, settings)
		}
	}
	if err := ctx.Err(); err != nil {
		return imagePickerResult{}, err
	}
	if stateErr != nil {
		options.initialNote = "Could not restore the draft. Existing data is kept."
		if options.initial != nil {
			options.initialNote = "Could not read the draft. Keeping this session's choices."
		}
	}
	result, err := runImagePicker(ctx, input, output, options)
	// Unknown, corrupt, or inaccessible state remains untouched this session.
	if stateErr != nil {
		path = ""
	}
	if err != nil || result.Canceled {
		if result.changed {
			result.draftSaved = saveImagePickerExitDraft(ctx, os.Stderr, path, result.settings)
		}
		return result, err
	}
	err = finishImagePickerSelection(ctx, output, os.Stderr, path, &result)
	if err != nil && result.changed {
		result.draftSaved = saveImagePickerExitDraft(ctx, os.Stderr, path, result.settings)
	}
	return result, err
}

func restoreImagePickerDraft(options imagePickerOptions, settings imagePickerSettings) imagePickerOptions {
	if options.initial != nil {
		return options
	}
	options.initial = &settings
	if options.Prompt == "" {
		options.Prompt = settings.prompt
		// Restoring text must not turn queued Enter keys into a new request.
		options.resuming = options.resuming || settings.prompt != ""
	}
	return options
}

// Give controlled exits a short save budget, even after parent cancellation.
// Optional persistence and diagnostic failures must not change the exit status.
func saveImagePickerExitDraft(parent context.Context, diagnostics io.Writer, path string, settings imagePickerSettings) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Second)
	defer cancel()
	if path != "" {
		if err := saveImagePickerState(ctx, path, settings); err == nil {
			return true
		}
	}
	_, _ = fmt.Fprintln(diagnostics, "Could not save the draft.")
	return false
}

// Echo before saving the submitted draft or starting a request. Saving here
// means a slower API response cannot overwrite a newer interactive selection.
func finishImagePickerSelection(ctx context.Context, output, diagnostics io.Writer, path string, result *imagePickerResult) error {
	result.draftSaved = false
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
	} else {
		if _, err := fmt.Fprintln(output, command); err != nil {
			return imageSavingFailure("Could not print the command. No image request was started.", err)
		}
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
		if _, err := fmt.Fprintln(diagnostics, "Could not save the draft."); err != nil {
			return imageSavingFailure("Could not print the draft warning. No image request was started.", err)
		}
		return nil
	}
	result.draftSaved = true
	return nil
}
