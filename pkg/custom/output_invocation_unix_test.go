//go:build !windows

package custom

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestOutputInvocationStderrBrokenPipeKeepsFailureProvenance(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = write
	defer func() {
		os.Stderr = previous
		if err := write.Close(); err != nil {
			t.Error(err)
		}
	}()
	cause := cli.Exit("synthetic failure", 27)
	root := invocationCommand(&cli.Command{Name: "synthetic", Action: func(context.Context, *cli.Command) error { return cause }})
	err = RunWithOutputPolicy(t.Context(), root, func(ctx context.Context) error {
		return root.Run(ctx, []string{"openai", "--verbose", "synthetic"})
	})
	var diagnostic *diagnosticWriteError
	var exit cli.ExitCoder
	if !errors.Is(err, cause) || !errors.Is(err, syscall.EPIPE) || !errors.As(err, &diagnostic) ||
		!errors.As(err, &exit) || exit.ExitCode() != 27 || isOutputBrokenPipe(err) {
		t.Fatalf("stderr EPIPE hid the original failure or became stdout EPIPE: %v", err)
	}
}
