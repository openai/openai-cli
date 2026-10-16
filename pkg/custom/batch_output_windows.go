package custom

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func inheritBatchOutputLifeline(command *exec.Cmd, life *os.File) error {
	handle := syscall.Handle(life.Fd())
	if err := syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		return err
	}
	command.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{handle}}
	command.Args = append(command.Args, strconv.FormatUint(uint64(life.Fd()), 10))
	return nil
}
