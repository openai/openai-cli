package imagegallery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Inspection is a read-only snapshot of committed metadata and the selected
// font's private file, if it belongs to this gallery. It does not certify native
// font registration or rendering, and leaves pending attempts untouched.
type Inspection struct {
	State
	FontPath string
}

// Inspect never creates storage, recovers pending attempts, or removes files.
// Absent metadata reports an uninitialized gallery only when no earlier image,
// font or pending artifacts remain. The existing stable lock excludes
// cooperating writes while metadata and files are checked.
func Inspect(ctx context.Context, directory, tty, selectedFont string) (Inspection, error) {
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	if err := checkPrivate(directory, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Inspection{}, nil
		}
		return Inspection{}, err
	}
	lock, err := openLock(filepath.Join(directory, ".lock"), os.O_RDONLY)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, stateErr := os.Lstat(filepath.Join(directory, "state.json")); errors.Is(stateErr, os.ErrNotExist) {
				return Inspection{}, (&Gallery{directory: directory}).checkUninitialized()
			}
		}
		return Inspection{}, err
	}
	defer lock.Close()
	defer unlockFile(lock)
	owner, err := readPrivate(filepath.Join(directory, ".tty"), 64)
	if err == nil && string(owner) != tty {
		return Inspection{}, errors.New("image gallery belongs to another Terminal tab")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Inspection{}, err
	}
	for _, name := range []string{"images", "fonts"} {
		if err := checkPrivate(filepath.Join(directory, name), true); err != nil {
			return Inspection{}, err
		}
	}
	g := &Gallery{directory: directory}
	if err := g.readState(); err != nil {
		return Inspection{}, err
	}
	result := Inspection{State: g.State()}
	if result.Initialized && selectedFont != "" {
		result.FontPath, err = g.LookupFontPS(ctx, selectedFont)
	}
	if err == nil {
		err = ctx.Err()
	}
	return result, err
}
