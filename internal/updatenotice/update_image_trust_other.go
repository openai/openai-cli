//go:build !darwin && !windows

package updatenotice

import "os"

// These platforms do not attach download security metadata.
func preservePreparedImageTrust(*os.File, *os.File) error { return nil }
