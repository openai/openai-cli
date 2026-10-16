//go:build !windows

package custom

import "os"

func prepareAdminSetupOutput(_ *os.File) (func() error, error) {
	return func() error { return nil }, nil
}
