//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package updatenotice

import "os"

func tryLock(*os.File) (bool, error) { return false, errCache }
