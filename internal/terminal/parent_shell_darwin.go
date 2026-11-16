package terminal

import (
	"os"

	"golang.org/x/sys/unix"
)

// ParentProcessName returns the immediate parent process name.
func ParentProcessName() (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getppid())
	if err != nil {
		return "", err
	}
	return unix.ByteSliceToString(info.Proc.P_comm[:]), nil
}
