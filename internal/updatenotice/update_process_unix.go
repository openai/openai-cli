//go:build darwin || linux

package updatenotice

import (
	"os"
	"os/exec"
	"syscall"
)

func automaticPlatform() bool { return os.Geteuid() != 0 }

func ownedUpdatePath(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0022 == 0 && (info.IsDir() || stat.Nlink == 1)
}

func startUpdateWorker(path, current string) {
	command := exec.Command(path, workerArgument, current)
	command.Env = append(updateEnvironment(), "OPENAI_CLI_UPDATE_WORKER=1")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// nil streams connect to the null device. No terminal handles or pipes
	// survive in the child. The worker has its own two-minute deadline.
	if command.Start() == nil {
		go func() { _ = command.Wait() }()
	}
}

func isolateVerifier(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
