//go:build !darwin && !linux

package admin

import (
	"os"
	"runtime"
)

const adminSetupInputSupported = runtime.GOOS == "windows"

func flushAdminSetupInput(_ *os.File) error {
	if !adminSetupInputSupported {
		return errAdminSetupUnsupported
	}
	// Windows drains its record queue in the native reader before Close returns.
	return nil
}
