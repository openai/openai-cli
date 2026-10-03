package custom

import (
	"os"

	"golang.org/x/sys/windows"
)

func imagePickerParentProcessName() (string, error) {
	parent, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getppid()))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(parent)
	var path [32768]uint16
	size := uint32(len(path))
	if err := windows.QueryFullProcessImageName(parent, 0, &path[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(path[:size]), nil
}
