//go:build !linux && !darwin

package updatenotice

import "os"

func openManagedBin() (*os.Root, error) { return nil, errUpdate }
