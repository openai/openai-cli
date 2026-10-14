//go:build !darwin && !linux

package custom

import (
	"context"
	"os/exec"
)

func configureInteractiveImageDiagnostic(context.Context, *exec.Cmd) {}
