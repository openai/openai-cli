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
		var available uint32
		r, _, _ := procPeekNamedPipe.Call(fd, 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
		if r == 0 || available > 0 {
			return r != 0 && available > 0
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}
