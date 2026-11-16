//go:build !darwin && !linux

package terminal

import (
	"context"
	"os/exec"
)

func configureOutputProcess(context.Context, *exec.Cmd) {}
