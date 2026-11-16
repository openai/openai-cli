//go:build !windows

package batches

import "golang.org/x/sys/unix"

func batchOutputFixtureHandleOpen(descriptor uintptr) bool {
	_, err := unix.FcntlInt(descriptor, unix.F_GETFD, 0)
	return err == nil
}
