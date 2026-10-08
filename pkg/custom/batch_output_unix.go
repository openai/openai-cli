//go:build !windows

package custom

import (
	"os"
	"os/exec"
)

func inheritBatchOutputLifeline(command *exec.Cmd, life *os.File) error {
	command.ExtraFiles = []*os.File{life}
	command.Args = append(command.Args, "3")
	return nil
}
