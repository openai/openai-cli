//go:build darwin

package terminalimage

import (
	"syscall"
	"unsafe"
)

// Used only inside the private worker. Never combine these constant dispositions
// with os/signal SIGINT handling or restore a Go handler: its runtime bookkeeping
// and Darwin's user trampoline are deliberately outside this isolated process.
func kittyInterruptDisposition(ignore bool) error {
	// Darwin's kernel input is __sigaction, which includes a trampoline slot
	// absent from libc's public sigaction structure. Both supported ports are
	// 64-bit. Constant SIG_DFL/SIG_IGN need no trampoline, mask or flags.
	var action struct {
		handler, trampoline uintptr
		mask                uint32
		flags               int32
	}
	if ignore {
		action.handler = 1 // SIG_IGN; zero is SIG_DFL.
	}
	_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION, uintptr(syscall.SIGINT), uintptr(unsafe.Pointer(&action)), 0)
	if errno != 0 {
		return errno
	}
	return nil
}
