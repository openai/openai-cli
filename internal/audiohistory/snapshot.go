package audiohistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
)

// Snapshot owns a read-only copy of one verified result. Consume File directly;
// File.Name is not a replay path. Unix snapshots have no directory entry.
type Snapshot struct {
	Entry Entry
	File  *os.File
	root  *os.Root
	name  string
	info  os.FileInfo
	once  sync.Once
	err   error
}

func (s *Snapshot) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.err = s.File.Close()
		if s.name != "" {
			s.err = errors.Join(s.err, removeOwned(s.root, s.name, s.info))
		}
		s.err = errors.Join(s.err, s.root.Close())
	})
	return s.err
}

// Open verifies the manifest and original bytes before exposing a private copy.
// Later replacement, deletion, or rewriting of history cannot change that copy.
func (s Store) Open(ctx context.Context, id string) (snapshot *Snapshot, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, ErrInvalid
	}
	root, err := s.open(false)
	if err != nil {
		return nil, err
	}
	keepRoot := false
	defer func() {
		if !keepRoot {
			err = errors.Join(err, root.Close())
		}
	}()
	directory, identity, err := openEntryDirectory(root, id)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entry, _, err := readManifest(ctx, directory, id)
	if err != nil {
		return nil, err
	}
	name, err := newID()
	if err != nil {
		return nil, err
	}
	name = ".snapshot-" + name
	file, info, err := createPrivateFile(root, name)
	if err != nil {
		return nil, err
	}
	keepFile := false
	defer func() {
		if !keepFile {
			err = errors.Join(err, removeOwned(root, name, info))
		}
	}()
	_, copyErr := copyVerified(ctx, directory, entry, file)
	completed, statErr := file.Stat()
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(copyErr, statErr, syncErr, closeErr, ctx.Err()); err != nil {
		return nil, err
	}
	if err := verifyDirectory(root, id, identity); err != nil {
		return nil, err
	}
	reader, readerInfo, err := openPrivateFile(root, name)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, readerInfo) || readerInfo.Size() != entry.Bytes || !readerInfo.ModTime().Equal(completed.ModTime()) {
		return nil, errors.Join(ErrChanged, reader.Close())
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, reader.Close())
	}
	snapshot = &Snapshot{Entry: entry, File: reader, root: root, name: name, info: readerInfo}
	if runtime.GOOS != "windows" {
		if err := removeOwned(root, name, readerInfo); err != nil {
			return nil, errors.Join(err, reader.Close())
		}
		snapshot.name = ""
	}
	keepRoot, keepFile = true, true
	return snapshot, nil
}

func copyVerified(ctx context.Context, directory *os.Root, entry Entry, destination io.Writer) (identity os.FileInfo, err error) {
	file, original, err := openPrivateFile(directory, "audio")
	if err != nil {
		return nil, errors.Join(ErrChanged, err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if original.Size() != entry.Bytes {
		return nil, ErrChanged
	}
	digest := sha256.New()
	count, err := copyContext(ctx, io.MultiWriter(destination, digest), file)
	if err != nil {
		return nil, err
	}
	current, err := file.Stat()
	if err != nil || count != entry.Bytes || current.Size() != original.Size() ||
		!current.ModTime().Equal(original.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
		return nil, errors.Join(err, ErrChanged)
	}
	return original, errors.Join(verifyUnchangedFile(directory, "audio", original), ctx.Err())
}

// Clear removes complete, validated history entries visible during its scan.
// Pending captures, snapshots, corrupt entries, and unrelated files remain.
// Exported files are outside Store and are never visited.
func (s Store) Clear(ctx context.Context) error {
	root, err := s.open(false)
	if errors.Is(err, os.ErrNotExist) {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return scanEntries(ctx, root, func(id string) error {
		directory, identity, err := openEntryDirectory(root, id)
		if errors.Is(err, os.ErrNotExist) {
			return nil // Another clear removed the enumerated directory.
		}
		if err != nil {
			return err
		}
		defer directory.Close()
		entry, manifestInfo, err := readManifest(ctx, directory, id)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		// Never recursively delete an entry: an unexpected child is preserved.
		children, err := directory.Open(".")
		if err != nil {
			return err
		}
		names, readErr := children.Readdirnames(3)
		closeErr := children.Close()
		if readErr != nil && readErr != io.EOF || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		for _, name := range names {
			if name == ".manifest" {
				pending, err := directory.Lstat(name)
				if errors.Is(err, os.ErrNotExist) {
					return nil // Capture just removed its temporary manifest link.
				}
				if err == nil && privateMode(pending, false) && os.SameFile(pending, manifestInfo) {
					return nil // Capture owns this short activation window.
				}
				return errors.Join(ErrInvalid, err)
			}
		}
		if len(names) != 2 || !(names[0] == "audio" && names[1] == "manifest.json" || names[1] == "audio" && names[0] == "manifest.json") {
			return ErrInvalid
		}
		audio, err := copyVerified(ctx, directory, entry, io.Discard)
		if err != nil {
			return err
		}
		if err := errors.Join(verifyDirectory(root, id, identity),
			verifyUnchangedFile(directory, "manifest.json", manifestInfo),
			verifyUnchangedFile(directory, "audio", audio), ctx.Err()); err != nil {
			return err
		}
		// Remove the manifest first so interrupted clearing never advertises a
		// result whose bytes have already been removed.
		if err := removeOwned(directory, "manifest.json", manifestInfo); err != nil {
			return err
		}
		return cleanupDirectory(root, id, directory, identity, map[string]os.FileInfo{"audio": audio})
	})
}
