//go:build !windows

package admin

import "os"

func prepareAdminSetupOutput(_ *os.File) (func() error, error) {
	return func() error { return nil }, nil
}
