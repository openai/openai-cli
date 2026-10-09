//go:build !darwin && !linux

package custom

import "os"

func flushAdminSetupInput(_ *os.File) error {
	// Windows drains its record queue in the reader. Other platforms retain
	// their existing cleanup behavior.
	return nil
}
