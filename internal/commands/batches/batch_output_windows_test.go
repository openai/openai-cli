package batches

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func batchOutputFixtureHandleOpen(descriptor uintptr) bool {
	var flags uint32
	function := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	ok, _, _ := function.Call(descriptor, uintptr(unsafe.Pointer(&flags)))
	return ok != 0
}
