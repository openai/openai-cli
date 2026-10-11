package custom

import (
	"errors"

	"golang.org/x/sys/unix"
)

func fileReceiptGoCallerName(pid int) (string, error) {
	launcher, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	parent := int(launcher.Eproc.Ppid)
	if parent <= 1 || parent == pid {
		return "", errors.New("Go launcher parent is unavailable")
	}
	caller, err := unix.SysctlKinfoProc("kern.proc.pid", parent)
	if err != nil {
		return "", err
	}
	return unix.ByteSliceToString(caller.Proc.P_comm[:]), nil
}
