package updatenotice

import (
	"os"
	"path/filepath"
	"syscall"
)

// A package installation can fetch the pinned verifier when another user can
// replace the selected program. Never execute such a program, even for --help.
func trustedSystemVerifier(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	for name := path; ; name = filepath.Dir(name) {
		info, err := os.Lstat(name)
		if err != nil {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			return false
		}
		mode := info.Mode()
		if name == path {
			if !mode.IsRegular() || mode.Perm()&0022 != 0 || mode.Perm()&0111 == 0 {
				return false
			}
		} else if !info.IsDir() || (mode.Perm()&0022 != 0 && mode&os.ModeSticky == 0) {
			// In a sticky directory, only the directory owner, child owner, or
			// root can replace an existing child. We check both owners.
			return false
		}
		if name == filepath.Dir(name) {
			return true
		}
	}
}
