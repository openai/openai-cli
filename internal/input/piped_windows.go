//go:build windows

package input

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procPeekNamedPipe = kernel32.NewProc("PeekNamedPipe")
)

func pipedDataAvailable(file *os.File) bool {
	return pipedDataAvailableWindows(file.Fd())
}

func pipedDataAvailableWindows(fd uintptr) bool {
	// Match Unix's short probe window: a shell producer may not have written
	// when the CLI starts. Keep this bounded for terminals with an empty,
	// open stdin pipe, and never consume bytes while probing.
	deadline := time.Now().Add(10 * time.Millisecond)
	for {
		available, err := PeekPipe(fd)
		if err != nil || available > 0 {
			return err == nil && available > 0
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// PeekPipe reports available bytes without consuming or changing the borrowed handle.
// Callers using os.File.SyscallConn may pass its Control handle without calling Fd.
func PeekPipe(fd uintptr) (uint32, error) {
	var available uint32
	r, _, cause := procPeekNamedPipe.Call(fd, 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
	if r == 0 {
		return available, cause
	}
	return available, nil
}
