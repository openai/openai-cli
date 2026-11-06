package updatenotice

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// A package installation can fetch the pinned verifier when another user can
// replace the selected program. Never execute such a program, even for --help.
func trustedSystemVerifier(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	// Check the actual route, including the parents of symlink targets.
	// This supports /bin and ordinary alternatives without trusting a link
	// in a directory where another account could replace it.
	dir := "/"
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	links := 0
	root, err := os.Lstat(dir)
	if err != nil || !trustedVerifierOwner(root) || !protectedVerifierDir(root) {
		return false
	}
	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			dir = filepath.Dir(dir)
			continue
		}
		name := filepath.Join(dir, part)
		info, err := os.Lstat(name)
		if err != nil || !trustedVerifierOwner(info) {
			return false
		}
		mode := info.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			links++
			target, err := os.Readlink(name)
			if err != nil || links > 40 {
				return false
			}
			if filepath.IsAbs(target) {
				dir = "/"
			}
			parts = append(strings.Split(target, "/"), parts...)
		case mode.IsDir():
			if !protectedVerifierDir(info) {
				return false
			}
			dir = name
		case mode.IsRegular():
			return len(parts) == 0 && mode.Perm()&0022 == 0 && mode.Perm()&0111 != 0
		default:
			return false
		}
	}
	return false
}

func trustedVerifierOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()))
}

func protectedVerifierDir(info os.FileInfo) bool {
	// A sticky directory restricts replacement of its trusted owner's child.
	return info.IsDir() && (info.Mode().Perm()&0022 == 0 || info.Mode()&os.ModeSticky != 0)
}
