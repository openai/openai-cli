package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// An automatic offer is allowed only on the same interactive terminal as the
// request's input. In particular, a drained pipe is still not a terminal.
// Callers select this path only for readable output with previews enabled.
func prepareInteractiveImageFont(ctx context.Context, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !localAppleImageTerminal(runtime.GOOS, os.Getenv) || !isTerminal(out) {
		return nil
	}
	fallback := func(ctx context.Context) error { return preflightImageFont(ctx, out) }
	if !isTerminal(os.Stdin) {
		return fallback(ctx)
	}
	outTTY, err := imageFontTTY(ctx, out.(*os.File))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fallback(ctx)
	}
	inTTY, err := imageFontTTY(ctx, os.Stdin)
	if err != nil || !imageInlineOfferSameTTY(inTTY, outTTY) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fallback(ctx)
	}
	return runImageInlineOffer(ctx, out, true, imageInlineOfferServices{
		ready: func(ctx context.Context) (bool, error) { return imageFontReady(ctx, out) },
		confirm: func(ctx context.Context) (bool, error) {
			return confirmImageInlineTTY(ctx, outTTY, os.Stdin)
		},
		setup: func(ctx context.Context) error {
			dir, err := imageFontDirectory()
			if err != nil {
				return err
			}
			return setupCurrentImageFont(ctx, out, dir, outTTY, nativeImageFontServices())
		},
		fallback: fallback,
	})
}

func imageInlineOfferSameTTY(input, output string) bool {
	return input != "" && input == output
}

type imageInlineOfferServices struct {
	ready    func(context.Context) (bool, error)
	confirm  func(context.Context) (bool, error)
	setup    func(context.Context) error
	fallback func(context.Context) error
}

// Keep the decision separate from terminal access so policy tests never read
// the user's input, change a profile, or call the API.
func runImageInlineOffer(ctx context.Context, out io.Writer, interactive bool, services imageInlineOfferServices) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !interactive {
		return services.fallback(ctx)
	}
	ready, err := services.ready(ctx)
	if err != nil || ready {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := fmt.Fprint(out, "Enable sharp images in THIS Apple Terminal tab?\nKeeps your text style, font size, profile and colors.\nmacOS may ask for Terminal automation permission. [y/N] "); err != nil {
		return err
	}
	accepted, err := services.confirm(ctx)
	if _, writeErr := fmt.Fprintln(out); err == nil {
		err = writeErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if !accepted {
		_, err := fmt.Fprintf(out, "Using a text preview for this command. Enable sharp images later with:\n  %s images inline setup\n", imageInlineExecutable())
		return err
	}
	return services.setup(ctx)
}

func confirmImageInlineTTY(ctx context.Context, tty string, original *os.File) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Own a descriptor for the already-verified terminal; never close or replace
	// os.Stdin. Recheck its identity before any input can be consumed.
	input, err := os.Open(tty)
	if err != nil {
		return false, errors.New("could not read the setup choice; run openai images inline setup explicitly")
	}
	defer input.Close()
	before, beforeErr := original.Stat()
	after, afterErr := input.Stat()
	if beforeErr != nil || afterErr != nil || !os.SameFile(before, after) || !isTerminal(input) {
		return false, errors.New("terminal input changed; run openai images inline setup explicitly")
	}
	return readImageInlineChoice(ctx, input)
}

// The fixed child reads one canonical terminal line and only returns a choice.
// Input is never evaluated as shell code or copied into an error. Passing an
// *os.File directly avoids a background goroutine reading stdin; cancellation
// kills and waits for the child before returning, leaving no pending reader.
func readImageInlineChoice(ctx context.Context, input *os.File) (bool, error) {
	const script = "IFS=' \t\n' read -r answer || exit 2\ncase \"$answer\" in [Yy]|[Yy][Ee][Ss]) exit 0 ;; *) exit 1 ;; esac"
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	command.Stdin = input
	command.Env = []string{"PATH=/usr/bin:/bin"}
	err := command.Run()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 2) {
		return false, nil
	}
	return false, errors.New("could not read the setup choice; run openai images inline setup explicitly")
}
