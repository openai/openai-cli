//go:build !linux

package updatenotice

import "os"

func openVerifierBin() (*os.Root, error) { return nil, errUpdate }
