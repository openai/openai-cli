package custom

import (
	"context"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
	audiocommands "github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/urfave/cli/v3"
)

const imagePickerFirstRunTimeout = 250 * time.Millisecond

// A stalled filesystem operation must not accumulate workers if setup is
// requested again in the same process. The worker releases its slot on return.
var imagePickerFirstRunWorker = make(chan struct{}, 1)

// SetupImagePickerShellOnFirstRun quietly prepares future interactive shells.
// It does not change the current command, terminal bindings, or exit status.
// Failures remain optional, and later ordinary runs may safely retry setup.
func SetupImagePickerShellOnFirstRun(ctx context.Context, args []string, roots ...*cli.Command) {
	inputTTY, outputTTY, errorTTY := term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stdout.Fd()), term.IsTerminal(os.Stderr.Fd())
	if ctx.Err() != nil || !inputTTY || !outputTTY || !errorTTY {
		return
	}
	// Read command declarations before the optional worker starts. CLI parsing
	// may mutate them after the foreground timeout, so the worker borrows none.
	if len(roots) > 0 && audiocommands.SkipFirstRunSetup(roots[0], args) {
		return
	}
	args = append([]string(nil), args...)
	waitForImagePickerFirstRun(ctx, func(ctx context.Context) {
		images.SetupShellOnFirstRun(ctx, args)
	})
}

// Filesystem calls cannot in general be interrupted by a context. Keep every
// such call off the foreground command path and bound only the foreground wait.
// A syscall already in progress may finish later; subsequent transaction steps
// check cancellation, and cleanup may remove uncommitted owned artifacts.
func waitForImagePickerFirstRun(ctx context.Context, setup func(context.Context)) {
	if ctx.Err() != nil {
		return
	}
	select {
	case imagePickerFirstRunWorker <- struct{}{}:
	default:
		return
	}
	ctx, cancel := context.WithTimeout(ctx, imagePickerFirstRunTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { <-imagePickerFirstRunWorker }()
		if ctx.Err() == nil {
			setup(ctx)
		}
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}
