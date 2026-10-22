// Package models owns model-list selection and interactive presentation.
package models

import (
	"context"

	"github.com/urfave/cli/v3"
)

// Options supplies application policy without coupling model behavior to assembly.
type Options struct {
	LoadingAllowed func(*cli.Command) bool
	// StartFeedback returns a non-nil, idempotent stop function that joins its worker.
	StartFeedback func(context.Context, *cli.Command) func()
}
