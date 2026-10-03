//go:build linux

package terminalimage

import (
	"runtime"
	"syscall"
	"unsafe"
)

// Used only inside the private worker, without os/signal SIGINT registration,
// Go handler restoration, or child processes. The kernel owns active SIGINT.
func kittyInterruptDisposition(ignore bool) error {
	// Only the handler is nonzero; every ABI's flags, restorer and mask stay
	// zero. Reserve more than the largest kernel action instead of assuming
	// identical field order on every Go Linux port. MIPS puts flags first and
	// uses a 128-bit mask; other ports put the handler first and use 64 bits.
	var action [16]uintptr
	maskSize, handler := uintptr(8), 0
	switch runtime.GOARCH {
	case "mips", "mipsle", "mips64", "mips64le":
		maskSize, handler = 16, 1
	}
	if ignore {
		action[handler] = 1 // SIG_IGN; zero is SIG_DFL.
	}
	_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(syscall.SIGINT), uintptr(unsafe.Pointer(&action[0])), 0, maskSize, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
