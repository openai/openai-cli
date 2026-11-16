package audiohistory

import (
	"context"
	"errors"
	"io"
	"os"
)

const clearPrefix = ".clear-"

// Clear removes complete, validated history entries visible during its scan.
// A private manifest link makes interrupted deletions retryable. Unmarked
// pending captures, snapshots, corrupt entries, and unrelated files remain.
// Exported files are outside Store and are never visited.
func (s Store) Clear(ctx context.Context) error {
	return s.clear(ctx, (*os.Root).Remove)
}

func (s Store) clear(ctx context.Context, remove func(*os.Root, string) error) error {
	root, err := s.open(false)
	if errors.Is(err, os.ErrNotExist) {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return scanEntries(ctx, root, true, func(id string) error {
		return clearEntry(ctx, root, id, remove)
	})
}

func clearEntry(ctx context.Context, root *os.Root, id string, remove func(*os.Root, string) error) error {
	marker := clearPrefix + id
	entry, markerInfo, err := readManifestFile(ctx, root, marker, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory, identity, err := openEntryDirectory(root, id)
	if errors.Is(err, os.ErrNotExist) {
		if markerInfo != nil {
			return removeClearFile(ctx, root, marker, markerInfo, remove)
		}
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	defer directory.Close()

	var manifestInfo os.FileInfo
	if markerInfo == nil {
		entry, manifestInfo, err = readManifest(ctx, directory, id)
		if errors.Is(err, os.ErrNotExist) {
			return nil // A pending capture or another clear owns this directory.
		}
		if err != nil {
			return err
		}
	} else {
		manifestInfo, err = directory.Lstat("manifest.json")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if manifestInfo != nil {
			if err := verifyUnchangedFile(directory, "manifest.json", markerInfo); err != nil {
				return err
			}
		}
	}

	// Never recursively delete an entry or infer ownership from its name alone.
	children, err := directory.Open(".")
	if err != nil {
		return err
	}
	names, readErr := children.Readdirnames(3)
	closeErr := children.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	hasAudio := false
	for _, name := range names {
		switch name {
		case ".manifest":
			pending, err := directory.Lstat(name)
			if markerInfo == nil && (errors.Is(err, os.ErrNotExist) ||
				err == nil && privateMode(pending, false) && os.SameFile(pending, manifestInfo)) {
				return nil // Capture owns this short activation window.
			}
			return errors.Join(ErrInvalid, err)
		case "audio":
			hasAudio = true
		case "manifest.json":
		default:
			return ErrInvalid
		}
	}
	if markerInfo == nil && len(names) != 2 || manifestInfo != nil && !hasAudio {
		return ErrInvalid
	}
	var audio os.FileInfo
	if hasAudio {
		audio, err = copyVerified(ctx, directory, entry, io.Discard)
		if err != nil && !(markerInfo != nil && errors.Is(err, os.ErrNotExist)) {
			return err
		}
	}
	if err := errors.Join(verifyDirectory(root, id, identity), ctx.Err()); err != nil {
		return err
	}
	if audio != nil {
		if err := verifyUnchangedFile(directory, "audio", audio); err != nil {
			return err
		}
	}
	if markerInfo == nil {
		if err := verifyUnchangedFile(directory, "manifest.json", manifestInfo); err != nil {
			return err
		}
		// An exclusive link preserves the exact verified manifest until every
		// deletion succeeds, including removal of the entry directory itself.
		if err := root.Link(id+"/manifest.json", marker); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		markerInfo = manifestInfo
	}
	if err := errors.Join(verifyUnchangedFile(root, marker, markerInfo), verifyDirectory(root, id, identity), ctx.Err()); err != nil {
		return err
	}
	// Hide the completed result before removing audio. The root marker stays
	// discoverable if any later removal fails or the process is interrupted.
	if err := removeClearFile(ctx, directory, "manifest.json", markerInfo, remove); err != nil {
		return err
	}
	if audio != nil {
		if err := removeClearFile(ctx, directory, "audio", audio, remove); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyDirectory(root, id, identity); err == nil {
		if err := remove(root, id); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeClearFile(ctx, root, marker, markerInfo, remove)
}

func removeClearFile(ctx context.Context, root *os.Root, name string, identity os.FileInfo, remove func(*os.Root, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyUnchangedFile(root, name, identity); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // Another clear completed this removal.
		}
		return err
	}
	if err := remove(root, name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
