//go:build !windows

package autocomplete

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// The same startup file can be reached through trusted directory aliases, such
// as /var and /private/var on Darwin. The profile itself must remain a regular
// file, and every resolved name must refer to the validated root and file.
func pickerProfileIdentity(ctx context.Context, root *os.Root, profile string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	directory, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer directory.Close()
	parentInfo, err := os.Stat(filepath.Dir(profile))
	opened, rootErr := directory.Stat()
	if err != nil || rootErr != nil || !os.SameFile(parentInfo, opened) {
		return "", errors.Join(errPickerInstallChanged, err, rootErr)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parent, err := pickerUnixFilePath(directory, filepath.Dir(profile))
	if err != nil {
		return "", err
	}
	parentInfo, err = os.Stat(parent)
	if err != nil || !os.SameFile(parentInfo, opened) {
		return "", errors.Join(errPickerInstallChanged, err)
	}
	name := filepath.Base(profile)
	canonical := filepath.Join(parent, name)
	before, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return canonical, ctx.Err()
	}
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("shell integration requires a regular startup file")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() || !os.SameFile(before, fileInfo) {
		return "", errors.Join(errPickerInstallChanged, err)
	}
	canonical, err = pickerUnixFilePath(file, canonical)
	if err != nil {
		return "", err
	}
	current, err := os.Lstat(canonical)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(before, current) {
		return "", errors.Join(errPickerInstallChanged, err)
	}
	parentInfo, err = os.Stat(filepath.Dir(canonical))
	current, statErr := root.Lstat(name)
	if err != nil || statErr != nil || !os.SameFile(parentInfo, opened) || !current.Mode().IsRegular() || !os.SameFile(before, current) {
		return "", errors.Join(errPickerInstallChanged, err, statErr)
	}
	return canonical, ctx.Err()
}
