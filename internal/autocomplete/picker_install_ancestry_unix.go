//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package autocomplete

import (
	"errors"
	"os"
	"syscall"
)

func pickerAncestorOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()))
}

func checkPickerAncestorDirectory(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	// Sticky directories protect names owned by this user or root, whose
	// ownership is checked as the walk opens the next directory or alias.
	if !info.IsDir() || !pickerAncestorOwned(info) || info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return errors.New("shell integration requires protected directory ancestors")
	}
	return checkPickerDirectoryPermissions(file)
}

func checkPickerAncestorLink(_ *os.Root, _ string, info os.FileInfo) error {
	if !pickerAncestorOwned(info) {
		return errors.New("shell integration requires trusted symbolic link ancestors")
	}
	return nil
}
