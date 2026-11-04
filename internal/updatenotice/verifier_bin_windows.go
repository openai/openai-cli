package updatenotice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openManagedBin() (*os.Root, error) { return openWindowsManagedBin() }

func safeVerifierStore(root *os.Root) bool {
	opened, err := root.Open(".")
	if err != nil {
		return false
	}
	defer opened.Close()
	if err := checkWindowsUpdateHandle(opened, true, true); err != nil {
		return false
	}
	// The store lock is opened for writing by openCache before pin verification.
	err = inspectWindowsUpdatePath(filepath.Join(root.Name(), ".lock"), false, true)
	return err == nil || errors.Is(err, os.ErrNotExist)
}

func trustedSystemVerifier(path string) bool {
	if !filepath.IsAbs(path) || len(filepath.VolumeName(path)) != 2 || strings.Contains(path[2:], ":") {
		return false
	}
	drive, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	if err != nil || windows.GetDriveType(drive) != windows.DRIVE_FIXED {
		return false
	}
	cwd, err := os.Getwd()
	return err == nil && !sameWindowsName(filepath.Dir(path), cwd) && validateWindowsPath(path, false, false) == nil
}
