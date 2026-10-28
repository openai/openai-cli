//go:build windows

package autocomplete

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows can reach the same startup file through different case and short-name
// spellings. Use its actual long path for script names and transaction locks,
// without conflating distinct files in a case-sensitive directory.
func pickerProfileIdentity(ctx context.Context, root *os.Root, profile string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	directory, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer directory.Close()
	parent, err := pickerWindowsHandlePath(ctx, directory)
	if err != nil {
		return "", err
	}
	name := filepath.Base(profile)
	before, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Join(parent, name), ctx.Err()
	}
	if err != nil {
		return "", err
	}
	attributes, known := before.Sys().(*syscall.Win32FileAttributeData)
	if !before.Mode().IsRegular() || !known || attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", errors.New("shell integration requires a regular startup file")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return "", errors.Join(errPickerInstallChanged, err)
	}
	canonical, err := pickerWindowsHandlePath(ctx, file)
	if err != nil {
		return "", err
	}
	parentInfo, err := os.Stat(filepath.Dir(canonical))
	rootInfo, rootErr := directory.Stat()
	current, statErr := root.Lstat(name)
	if err != nil || rootErr != nil || statErr != nil || !os.SameFile(parentInfo, rootInfo) ||
		!current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return "", errors.Join(errPickerInstallChanged, err, rootErr, statErr)
	}
	return canonical, ctx.Err()
}

func pickerWindowsHandlePath(ctx context.Context, file *os.File) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &attributes); err != nil ||
		attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", errors.New("cannot inspect shell startup file identity")
	}
	buffer := make([]uint16, 260)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		length, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", errors.New("cannot inspect shell startup file identity")
		}
		if length >= uint32(len(buffer)) {
			buffer = make([]uint16, length)
			continue
		}
		path := windows.UTF16ToString(buffer[:length])
		if strings.HasPrefix(path, `\\?\UNC\`) {
			path = `\\` + path[len(`\\?\UNC\`):]
		} else if strings.HasPrefix(path, `\\?\`) {
			path = path[len(`\\?\`):]
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// EvalSymlinks also normalizes each Windows component using its actual
		// long filename, including casing. The caller already checked ancestry
		// and disallowed reparse points; confirm this name still reaches our handle.
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		current, err := os.Stat(canonical)
		opened, statErr := file.Stat()
		if err != nil || statErr != nil || !os.SameFile(current, opened) {
			return "", errors.Join(errPickerInstallChanged, err, statErr)
		}
		return canonical, ctx.Err()
	}
}
