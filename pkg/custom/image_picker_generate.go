package custom

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// Only a bare interactive command enters the picker. Explicit flags and piped
// bodies retain their existing validation, defaults and output behavior.
func imagePickerWorkflow(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		out, ok := command.Root().Writer.(*os.File)
		if !ok || out != os.Stdout || !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(out.Fd()) ||
			strings.EqualFold(os.Getenv("TERM"), "dumb") || !imagePickerRequested(command) {
			return next(ctx, command)
		}
		options := imagePickerOptions{}
		for {
			result, err := runImagePickerSession(ctx, os.Stdin, out, options)
			if err != nil {
				return err
			}
			if result.Canceled {
				code := result.ExitCode
				if code == 0 {
					code = 130
				}
				return cli.Exit("", code)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if result.PrintOnly {
				return nil
			}
			if err := runImagePickerAction(ctx, command, result.Args, next); err != nil {
				return err
			}
			// Reopen below the saved result, retaining this session's choices
			// until the user exits.
			options.initial, options.resuming = &result.settings, true
		}
	}
}

func runImagePickerAction(ctx context.Context, command *cli.Command, args []string, next cli.ActionFunc) error {
	for i := 2; i < len(args); i += 2 {
		restore, err := setImagePickerFlag(command, strings.TrimPrefix(args[i], "--"), args[i+1])
		if err != nil {
			return err
		}
		defer restore()
	}
	// Each request uses the existing saving workflow and generated SDK action.
	// Restore flags before the next draft, including after failed requests.
	return next(ctx, command)
}

// Isolate selections from the shared command tree, including after a failed
// action. A struct copy alone would still share the flag's parsed value.
func setImagePickerFlag(command *cli.Command, name, value string) (func(), error) {
	for _, candidate := range command.Flags {
		if !slices.Contains(candidate.Names(), name) {
			continue
		}
		switch flag := candidate.(type) {
		case *requestflag.Flag[string]:
			return setTemporaryImagePickerFlag(flag, name, value)
		case *requestflag.Flag[*string]:
			return setTemporaryImagePickerFlag(flag, name, value)
		case *requestflag.Flag[*int64]:
			return setTemporaryImagePickerFlag(flag, name, value)
		case *cli.StringFlag:
			original, replacement := *flag, *flag
			// Do not mutate an existing parsed value or external destination.
			replacement.Destination = nil
			if err := replacement.PreParse(); err != nil {
				return nil, err
			}
			if err := replacement.Set(name, value); err != nil {
				return nil, err
			}
			*flag = replacement
			return func() { *flag = original }, nil
		}
		break
	}
	return nil, fmt.Errorf("unsupported image picker setting: %s", name)
}

func setTemporaryImagePickerFlag[T string | *string | *int64](flag *requestflag.Flag[T], name, value string) (func(), error) {
	original, replacement := *flag, *flag
	if err := replacement.PreParse(); err != nil {
		return nil, err
	}
	if err := replacement.Set(name, value); err != nil {
		return nil, err
	}
	*flag = replacement
	return func() { *flag = original }, nil
}

func imagePickerRequested(command *cli.Command) bool {
	if command.Args().Len() != 0 {
		return false
	}
	for _, flag := range command.Flags {
		if flag.IsSet() {
			return false
		}
	}
	for _, name := range []string{"format", "format-error", "transform", "transform-error", "raw-output"} {
		if command.Root().IsSet(name) {
			return false
		}
	}
	return true
}
