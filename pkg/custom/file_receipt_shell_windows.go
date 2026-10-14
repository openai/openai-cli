package custom

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func fileReceiptGoCallerName(pid int) (string, error) {
	launcher, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(launcher)
	var info windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(launcher, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return "", err
	}
	parent := info.InheritedFromUniqueProcessId
	if parent <= 1 || parent == uintptr(pid) || parent > uintptr(^uint32(0)) {
		return "", errors.New("Go launcher parent is unavailable")
	}
	caller, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(parent))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(caller)
	var path [32768]uint16
	size := uint32(len(path))
	if err := windows.QueryFullProcessImageName(caller, 0, &path[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(path[:size]), nil
}
