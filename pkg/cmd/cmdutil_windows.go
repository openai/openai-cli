//go:build windows

package cmd

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procPeekNamedPipe = kernel32.NewProc("PeekNamedPipe")
)

func isPipedDataAvailableOSSpecific() bool {
	// On Windows, unix.Poll is not available. Use PeekNamedPipe to check if data is available
	// on the pipe without consuming it.
	var available uint32
	r, _, _ := procPeekNamedPipe.Call(
		os.Stdin.Fd(),
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&available)),
		0,
	)
	return r != 0 && available > 0
}

func streamOutputOSSpecific(label string, generateOutput func(w *os.File) error) error {
	// We have a trick with sockets that we use when possible on Unix-like systems. Those APIs aren't
	// available on Windows, so we fall back to using pipes.
	return streamToPagerWithPipe(label, generateOutput)
}
