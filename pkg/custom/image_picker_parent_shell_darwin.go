package custom

import (
	"os"

	"golang.org/x/sys/unix"
)

func imagePickerParentProcessName() (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getppid())
	if err != nil {
		return "", err
	}
	return unix.ByteSliceToString(info.Proc.P_comm[:]), nil
}
