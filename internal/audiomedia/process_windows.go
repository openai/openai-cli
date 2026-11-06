package audiomedia

import "os/exec"

// CommandContext terminates the direct helper on Windows. WaitDelay bounds pipe
// waits; this does not claim Windows descendant or native device validation.
func configureHelperProcess(command *exec.Cmd) {}

func stopHelperProcess(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
