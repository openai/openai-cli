//go:build !windows

package output

import "os"

// CreatePrivateDownloadFile exclusively creates private download staging inside root.
func CreatePrivateDownloadFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}
