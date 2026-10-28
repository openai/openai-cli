package autocomplete

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func openPickerProfileDirectory(ctx context.Context, path string, create bool) (*os.Root, error) {
	// Keep the trailing separator and dot: the profile's parent then receives
	// full ancestor validation, including trusted Unix aliases, before the
	// resolved directory is bound to its opened handle. filepath.Join cleans it
	// away. Managed script directories still reject a final symbolic link.
	return openPickerDirectory(ctx, path+string(os.PathSeparator)+".", create)
}

// Walk from the volume root through opened parents. A sourced script's lexical
// path and every symlink target must both remain protected from replacement.
// Missing components are created only below an already validated directory.
func openPickerDirectoryTree(ctx context.Context, path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("shell integration requires absolute directory paths")
	}
	links := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		volume := filepath.VolumeName(path)
		resolved := volume + string(os.PathSeparator)
		root, err := os.OpenRoot(resolved)
		if err != nil {
			return nil, err
		}
		fail := func(err error) (*os.Root, error) { root.Close(); return nil, err }
		parts := strings.FieldsFunc(path[len(volume):], func(r rune) bool {
			return r == rune(os.PathSeparator) || os.PathSeparator == '\\' && r == '/'
		})
		restart := ""
		for index := 0; ; index++ {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			directory, err := root.Open(".")
			if err != nil {
				return fail(err)
			}
			err = errors.Join(checkPickerAncestorDirectory(directory), directory.Close())
			if err != nil {
				return fail(err)
			}
			if index == len(parts) {
				return root, nil
			}
			name := parts[index]
			if name == "." {
				continue
			}
			if name == ".." {
				restart = filepath.Dir(resolved) + string(os.PathSeparator) + strings.Join(parts[index+1:], string(os.PathSeparator))
				break
			}
			before, err := root.Lstat(name)
			if errors.Is(err, os.ErrNotExist) && create {
				directory, openErr := root.Open(".")
				if openErr != nil {
					return fail(openErr)
				}
				err = errors.Join(checkPickerChildCreationPermissions(directory), directory.Close())
				if err != nil {
					return fail(err)
				}
				if err := ctx.Err(); err != nil {
					return fail(err)
				}
				if err := root.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
					return fail(err)
				}
				before, err = root.Lstat(name)
			}
			if err != nil {
				return fail(err)
			}
			if before.Mode()&os.ModeSymlink != 0 {
				if index == len(parts)-1 {
					return fail(errors.New("shell integration requires a directory, not a symbolic link"))
				}
				if err := checkPickerAncestorLink(root, name, before); err != nil {
					return fail(err)
				}
				target, err := root.Readlink(name)
				after, statErr := root.Lstat(name)
				if err != nil || statErr != nil || !os.SameFile(before, after) || after.Mode()&os.ModeSymlink == 0 {
					return fail(errors.Join(errPickerInstallChanged, err, statErr))
				}
				// Match filepath.EvalSymlinks' existing traversal ceiling. Keep
				// dot-dot components until traversal resolves earlier aliases.
				links++
				if links > 255 {
					return fail(errors.New("too many symbolic links in shell integration path"))
				}
				restart = target
				if !filepath.IsAbs(target) {
					restart = resolved + string(os.PathSeparator) + target
				}
				restart += string(os.PathSeparator) + strings.Join(parts[index+1:], string(os.PathSeparator))
				break
			}
			if !before.IsDir() {
				return fail(errors.New("shell integration requires directory ancestors"))
			}
			child, err := root.OpenRoot(name)
			if err != nil {
				return fail(err)
			}
			opened, openErr := child.Stat(".")
			after, statErr := root.Lstat(name)
			if openErr != nil || statErr != nil || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
				child.Close()
				return fail(errors.Join(errPickerInstallChanged, openErr, statErr))
			}
			root.Close()
			root = child
			resolved = filepath.Join(resolved, name)
		}
		root.Close()
		path = restart
	}
}
