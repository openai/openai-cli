//go:build linux || darwin

package updatenotice

import (
	"os"
	"path/filepath"
)

// The installer creates these private directories before provisioning. Never
// follow a replacement link into a shared or package-manager directory.
func openManagedBin() (*os.Root, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, errCache
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{".openai", "bin"} {
		parent, err := root.Stat(".")
		if err != nil || !ownedUpdatePath(parent) {
			root.Close()
			return nil, errCache
		}
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() || !ownedUpdatePath(info) {
			root.Close()
			return nil, errCache
		}
		child, err := root.OpenRoot(name)
		root.Close()
		if err != nil {
			return nil, err
		}
		actual, err := child.Stat(".")
		if err != nil || !os.SameFile(info, actual) {
			child.Close()
			return nil, errCache
		}
		root = child
	}
	return root, nil
}
