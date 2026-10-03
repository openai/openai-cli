package custom

import (
	"os"
	"strconv"
)

func imagePickerParentProcessName() (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(os.Getppid()) + "/exe")
}
