//go:build !windows

package commands

import (
	"context"
	"errors"

	"github.com/openai/openai-cli/internal/updatenotice"
	"github.com/urfave/cli/v3"
)

func RegisterArchiveInstall(*cli.Command) {}

func enableUnmanagedUpdates(_ context.Context, current string) (string, error) {
	return "", errors.New(updatenotice.EnrollmentSetupGuidance(current))
}
