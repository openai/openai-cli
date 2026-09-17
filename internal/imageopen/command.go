package imageopen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func viewerCommand(ctx context.Context, program, path string, detached bool) *exec.Cmd {
	var command *exec.Cmd
	if detached {
		command = exec.Command(program, path)
	} else {
		command = exec.CommandContext(ctx, program, path)
	}
	command.Env = viewerEnvironment(os.Environ())
	return command
}

type viewerProcess interface {
	Start() error
	Wait() error
}

// Observe fast launcher failures, but do not wait for an image window to close.
// A buffered result channel lets Wait reap the child after this call returns.
func startDetached(ctx context.Context, process viewerProcess, grace time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := process.Start(); err != nil {
		return fmt.Errorf("request default image viewer: %w", fileErrorCause(err))
	}
	finished := make(chan error, 1)
	go func() { finished <- process.Wait() }()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-finished:
		if err != nil {
			return fmt.Errorf("request default image viewer: %w", fileErrorCause(err))
		}
		return nil
	case <-ctx.Done():
		// The request has been handed off; cancellation must not close a viewer.
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func viewerEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(strings.ToUpper(entry), "OPENAI_") {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
