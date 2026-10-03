package autocomplete

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const pickerRecoveryLimit = 32

var errPickerProfilePublished = errors.New("shell startup was updated but recovery bookkeeping failed")

type pickerRecoveryManifest struct {
	Version int    `json:"version"`
	Profile string `json:"profile"`
}

func pickerRecoveryPrefix(name string) string {
	digest := sha256.Sum256([]byte(name))
	return ".openai-picker-recovery-" + hex.EncodeToString(digest[:8]) + "-"
}

// Existing files are moved, never overwritten or unlinked. Retain the actual
// captured inode even after successful verification: an editor can still have
// it open and write to it after publication. This deliberately trades a brief
// absent-profile interval for preserving those late edits.
func commitPickerProfile(ctx context.Context, root *os.Root, name string, previous pickerFileSnapshot, temporary string) error {
	if err := checkPickerSnapshot(ctx, root, name, previous); err != nil {
		return err
	}
	if previous.info == nil {
		return renamePickerFileNoReplace(root, temporary, name)
	}
	if err := checkPickerRecoveryCapacity(ctx, root, name); err != nil {
		return err
	}
	directory := pickerRecoveryPrefix(name) + rand.Text()
	if err := createPickerRecoveryDirectory(root, directory); err != nil {
		return err
	}
	recovery, err := openPickerRecoveryDirectory(root, directory)
	if err != nil {
		return err
	}
	defer recovery.Close()
	captured := false
	defer func() {
		if !captured {
			// Never remove original, including when capture's outcome is
			// uncertain. A nonempty directory cannot be removed here.
			removeEmptyPickerRecovery(root, recovery, directory)
		}
	}()
	manifest, err := json.Marshal(pickerRecoveryManifest{Version: 1, Profile: name})
	if err != nil {
		return err
	}
	if err := writePickerRecoveryFile(recovery, "manifest.json", manifest); err != nil {
		return err
	}
	if temporary != "" {
		if err := renamePickerFileNoReplace(root, temporary, filepath.Join(directory, "next")); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	original := filepath.Join(directory, "original")
	if err := renamePickerFileNoReplace(root, name, original); err != nil {
		return err
	}
	captured = true
	rollback := func(cause error) error {
		// Restoration is exclusive too: a third editor save takes priority.
		if err := renamePickerFileNoReplace(root, original, name); err != nil {
			return errors.Join(cause, errors.New("shell startup recovery copy was retained in .openai-picker-recovery-* beside the profile; inspect it before removing that directory"), err)
		}
		captured = false
		return cause
	}
	if err := checkPickerSnapshot(ctx, recovery, "original", previous); err != nil {
		return rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if temporary != "" {
		if err := renamePickerFileNoReplace(root, filepath.Join(directory, "next"), name); err != nil {
			return rollback(err)
		}
	}
	// From this point, publication/removal has committed. If recording that
	// fact fails, retain both the archive and the script for the next attempt.
	if err := writePickerRecoveryFile(recovery, "complete", nil); err != nil {
		return errors.Join(errPickerProfilePublished, err)
	}
	return nil
}

func checkPickerRecoveryCapacity(ctx context.Context, root *os.Root, name string) error {
	entries, err := pickerRecoveryEntries(ctx, root, name)
	if err != nil {
		return err
	}
	if len(entries) >= pickerRecoveryLimit {
		return errors.New("shell startup recovery storage is full; inspect and archive .openai-picker-recovery-* directories beside the profile before retrying")
	}
	return nil
}

func hasPendingPickerRecovery(ctx context.Context, root *os.Root, name string) (bool, error) {
	entries, err := pickerRecoveryEntries(ctx, root, name)
	if err != nil {
		return false, err
	}
	for _, directory := range entries {
		pending, err := isPendingPickerRecovery(root, directory)
		if err != nil {
			return false, err
		}
		if pending {
			return true, nil
		}
	}
	return false, nil
}

// Recovery runs under the profile lock before reading the current profile.
// An incomplete capture restores only an absent path. If an editor (or our
// publication before process exit) supplied a path, both files are retained.
// A completed uninstall is never restored. Pending uninstalls roll back, then
// the caller applies its current requested operation to the recovered file.
func recoverPickerProfiles(ctx context.Context, root *os.Root, name string) error {
	entries, err := pickerRecoveryEntries(ctx, root, name)
	if err != nil {
		return err
	}
	for _, directory := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := recoverPickerProfile(ctx, root, name, directory); err != nil {
			return err
		}
	}
	return nil
}

func recoverPickerProfile(ctx context.Context, root *os.Root, name, directory string) error {
	recovery, err := openPickerRecoveryDirectory(root, directory)
	if err != nil {
		return err
	}
	defer recovery.Close()
	manifest, err := readPickerFile(ctx, recovery, "manifest.json")
	if err != nil {
		return err
	}
	var record pickerRecoveryManifest
	if manifest.info == nil {
		// The process may have exited immediately after creating the private
		// directory. Only an empty, uncaptured transaction can be discarded.
		return root.Remove(directory)
	}
	if json.Unmarshal(manifest.data, &record) != nil || record.Version != 1 || record.Profile != name {
		return errors.New("shell startup recovery metadata needs manual inspection in .openai-picker-recovery-* beside the profile")
	}
	complete, err := readPickerFile(ctx, recovery, "complete")
	if err != nil {
		return err
	}
	if complete.info != nil {
		if len(complete.data) != 0 {
			return errPickerInstallChanged
		}
		return nil
	}
	_, err = recovery.Lstat("original")
	if errors.Is(err, os.ErrNotExist) {
		// Interrupted before capture, or after a successful restoration.
		return removeEmptyPickerRecovery(root, recovery, directory)
	}
	if err != nil {
		return err
	}
	if err := renamePickerFileNoReplace(root, filepath.Join(directory, "original"), name); err == nil {
		return removeEmptyPickerRecovery(root, recovery, directory)
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	return writePickerRecoveryFile(recovery, "complete", nil)
}

func pickerRecoveryEntries(ctx context.Context, root *os.Root, name string) ([]string, error) {
	_, profileErr := root.Lstat(name)
	profileMissing := errors.Is(profileErr, os.ErrNotExist)
	if profileErr != nil && !profileMissing {
		return nil, profileErr
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	var names []string
	otherPending := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := directory.ReadDir(64)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), pickerRecoveryPrefix(name)) {
				names = append(names, entry.Name())
				if len(names) > pickerRecoveryLimit {
					return nil, errors.New("too many shell startup recovery directories; inspect .openai-picker-recovery-* beside the profile")
				}
			} else if profileMissing && strings.HasPrefix(entry.Name(), ".openai-picker-recovery-") {
				// Once a file is absent, its canonical spelling cannot be
				// recovered through filesystem identity (case and Windows
				// short-name aliases included). Do not create a fresh profile
				// while a differently named capture could be its original.
				pending, err := isPendingPickerRecovery(root, entry.Name())
				if err != nil {
					return nil, err
				}
				otherPending = otherPending || pending
			}
		}
		if errors.Is(readErr, io.EOF) {
			if otherPending {
				for _, name := range names {
					pending, err := isPendingPickerRecovery(root, name)
					if err != nil {
						return nil, err
					}
					if pending {
						// Independent captures can recover using their exact
						// recorded names without blocking one another.
						return names, nil
					}
				}
				return nil, errors.New("another shell startup capture is unfinished; retry setup or removal using the profile spelling recorded in its .openai-picker-recovery-* manifest before creating a missing profile in this directory")
			}
			return names, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

func isPendingPickerRecovery(root *os.Root, name string) (bool, error) {
	recovery, err := openPickerRecoveryDirectory(root, name)
	if err != nil {
		return false, err
	}
	defer recovery.Close()
	_, completeErr := recovery.Lstat("complete")
	_, originalErr := recovery.Lstat("original")
	for _, err := range []error{completeErr, originalErr} {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return errors.Is(completeErr, os.ErrNotExist) && originalErr == nil, nil
}

func openPickerRecoveryDirectory(root *os.Root, name string) (*os.Root, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errPickerInstallChanged
	}
	recovery, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	file, err := recovery.Open(".")
	if err == nil {
		var opened os.FileInfo
		opened, err = file.Stat()
		if err == nil && !os.SameFile(info, opened) {
			err = errPickerInstallChanged
		}
		if err == nil {
			err = checkPickerRecoveryDirectory(file)
		}
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		recovery.Close()
		return nil, err
	}
	return recovery, nil
}

func writePickerRecoveryFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func removeEmptyPickerRecovery(root, recovery *os.Root, name string) error {
	if _, err := recovery.Lstat("original"); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("shell startup recovery original must be retained")
	}
	for _, file := range []string{"next", "complete", "manifest.json"} {
		if err := recovery.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return root.Remove(name)
}
