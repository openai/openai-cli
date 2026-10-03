package autocomplete

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// A deleted startup directory has no profile lock to acquire. Hold the script
// lock and confirm that the profile parent is still absent before removing any
// owned scripts; an installer needs this same lock before publishing a profile.
func removePickerScriptsWithoutProfile(ctx context.Context, options PickerInstallation) (bool, error) {
	scripts, err := openPickerScriptDirectory(ctx, options.Directory, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer scripts.Close()
	lock, err := lockPickerInstallation(ctx, scripts, ".picker-install.lock")
	if err != nil {
		return false, err
	}
	defer lock.Close()
	callerProfile := options.Profile
	options.Profile, err = pickerAbsentProfileIdentity(ctx, callerProfile)
	if err != nil {
		return false, err
	}
	return reconcilePickerScripts(ctx, scripts, options, callerProfile, pickerFileSnapshot{}, true)
}

// Recover aliases in the surviving prefix without guessing the spelling of
// deleted components. Namespaces that cannot be proved remain untouched.
func pickerAbsentProfileIdentity(ctx context.Context, profile string) (string, error) {
	missing := profile
	for {
		parent := filepath.Dir(missing)
		root, err := openPickerProfileDirectory(ctx, parent, false)
		if err == nil {
			defer root.Close()
			if missing == profile {
				return "", errPickerInstallChanged
			}
			prefix, err := pickerProfileIdentity(ctx, root, missing)
			if err != nil {
				return "", err
			}
			suffix, err := filepath.Rel(missing, profile)
			if err != nil {
				return "", err
			}
			return filepath.Join(prefix, suffix), nil
		}
		if !errors.Is(err, os.ErrNotExist) || parent == missing {
			return "", err
		}
		missing = parent
	}
}

// Scan in bounded batches so interrupted cleanup is discoverable without an
// ever-growing journal. Deleting callers hold the script lock and either the
// profile lock or proof of its absent parent; status only detects debt. Each
// removal still validates opened bytes/identity.
func reconcilePickerScripts(ctx context.Context, root *os.Root, options PickerInstallation, callerProfile string, active pickerFileSnapshot, remove bool) (changed bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	directory, err := root.Open(".")
	if err != nil {
		return false, err
	}
	defer directory.Close()
	caller := options
	caller.Profile = callerProfile
	for {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		names, readErr := directory.Readdirnames(64)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return changed, err
			}
			block := pickerInstalledBlock{Shell: options.Shell, Script: name}
			if !pickerScriptMatchesProfile(options, callerProfile, block) {
				continue
			}
			snapshot, err := readPickerFile(ctx, root, name)
			if ctx.Err() != nil {
				return changed, ctx.Err()
			}
			// Modified, linked, or protected files have no cleanup ownership.
			if err != nil || snapshot.info == nil || active.info != nil && os.SameFile(active.info, snapshot.info) {
				continue
			}
			if !validOwnedPickerScript(options, block, snapshot.data) && !validOwnedPickerScript(caller, block, snapshot.data) {
				continue
			}
			if !remove {
				return true, nil
			}
			if err := removePickerSnapshot(ctx, root, name, snapshot); err != nil {
				return changed, err
			}
			changed = true
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return changed, ctx.Err()
			}
			return changed, readErr
		}
	}
}
