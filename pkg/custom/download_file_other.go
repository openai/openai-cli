//go:build !windows

package custom

import "os"

func createPrivateDownloadFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}
