//go:build windows

package custom

import (
	"os"

	"golang.org/x/sys/windows"
)

func prepareAdminSetupOutput(output *os.File) (func() error, error) {
	handle := windows.Handle(output.Fd())
	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		return nil, errAdminSetupKeyIO
	}
	// The console must interpret paste controls before hidden input starts.
	mode := original | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := windows.SetConsoleMode(handle, mode); err != nil {
		return nil, errAdminSetupKeyIO
	}
	return func() error { return windows.SetConsoleMode(handle, original) }, nil
}
