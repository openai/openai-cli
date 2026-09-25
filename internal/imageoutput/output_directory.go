package imageoutput

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveDirectory returns an absolute, writable output directory. It creates
// the default directory, but requires a caller-selected directory to exist.
func ResolveDirectory(requested string) (string, error) {
	useDefault := requested == ""
	if useDefault || requested == "~" || strings.HasPrefix(requested, "~/") || strings.HasPrefix(requested, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve image output home directory: %w", err)
		}
		if useDefault {
			requested = filepath.Join(home, "Downloads", "gpt-images")
		} else if requested == "~" {
			requested = home
		} else {
			requested = filepath.Join(home, requested[2:])
		}
	}
	directory, err := filepath.Abs(requested)
	if err != nil {
		return "", imagePathError("resolve image output directory", requested, err)
	}
	if useDefault {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return "", imagePathError("create default image output directory", directory, err)
		}
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", imagePathError("open image output directory (choose an existing directory)", directory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("image output path is not a directory: %q", directory)
	}
	// A real write handles ACLs and read-only filesystems, unlike mode bits.
	probe, err := os.CreateTemp(directory, ".gpt-image-write-check-*")
	if err != nil {
		return "", imagePathError("image output directory is not writable", directory, err)
	}
	if err := removeProbe(probe); err != nil {
		return "", fmt.Errorf("check image output directory write access: %w", err)
	}
	return directory, nil
}

func removeProbe(probe *os.File) error {
	var failures []error
	info, statErr := probe.Stat()
	if err := probe.Close(); err != nil {
		failures = append(failures, imagePathError("close image write check", probe.Name(), err))
	}
	if statErr != nil {
		return errors.Join(append(failures, imagePathError("inspect image write check", probe.Name(), statErr))...)
	}
	if err := removeOwnedFile(probe.Name(), info); err != nil {
		failures = append(failures, imagePathError("remove image write check", probe.Name(), err))
	}
	return errors.Join(failures...)
}

// Do not remove a replacement if the user moved or replaced our file while it
// was open. Lstat also ensures cleanup never follows a replacement symlink.
func removeOwnedFile(path string, original os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(current, original) {
		return errors.New("image output path changed; leaving it untouched")
	}
	return os.Remove(path)
}

func imagePathError(operation, path string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
}
