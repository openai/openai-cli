package terminal

import (
	"os"
	"strconv"
)

// ParentProcessName returns the immediate parent process name.
func ParentProcessName() (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(os.Getppid()) + "/exe")
}
