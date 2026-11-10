//go:build !linux && !darwin && !windows

package updatenotice

import "os"

func openManagedBin() (*os.Root, error) { return nil, errUpdate }
