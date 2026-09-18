package imagefontmac

import "os"

// Supported reports whether the built-in macOS native bridge is executable.
func Supported() bool {
	info, err := os.Stat(interpreter)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}
