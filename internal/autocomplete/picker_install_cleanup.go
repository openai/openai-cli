package autocomplete

import (
	"context"
	"errors"
	"io"
	"os"
)

// Scan in bounded batches so interrupted cleanup is discoverable without an
// ever-growing journal. The profile and script locks protect deleting callers;
// status only detects debt. Each removal still validates opened bytes/identity.
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
