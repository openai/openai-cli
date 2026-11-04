//go:build !darwin

package updatenotice

import "os"

// These platforms do not attach macOS quarantine metadata.
func preservePreparedImageTrust(*os.File, *os.File) error { return nil }
