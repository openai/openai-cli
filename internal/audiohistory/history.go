// Package audiohistory stores private audio artifacts and creates verified,
// independent snapshots for replay and export.
package audiohistory

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// Settings records the explicitly private history of one audio request.
type Settings struct {
	Input        string  `json:"input"`
	Instructions string  `json:"instructions"`
	Model        string  `json:"model"`
	Voice        string  `json:"voice"`
	Format       string  `json:"format"`
	Speed        float64 `json:"speed"`
	Preview      bool    `json:"preview,omitempty"`
}

type Entry struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Settings  Settings  `json:"settings"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256"`
}

type Store struct{ Directory string }

type manifest struct {
	Version int   `json:"version"`
	Entry   Entry `json:"entry"`
}

var (
	ErrInvalid = errors.New("audio history metadata is unknown or invalid")
	ErrChanged = errors.New("audio history changed during the operation")
)

func Default() (Store, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Directory: filepath.Join(directory, "openai", "audio")}, nil
}

// Capture closes body on every exit. A manifest becomes visible only after the
// complete audio, source close, and metadata close have all succeeded.
func (s Store) Capture(ctx context.Context, body io.ReadCloser, settings Settings) (entry Entry, err error) {
	if body == nil {
		return Entry{}, errors.New("audio history capture requires a body")
	}
	source := &ownedBody{ReadCloser: body}
	defer func() { err = errors.Join(err, source.Close()) }()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(closed); _ = source.Close() })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if !validSettings(settings) {
		return Entry{}, ErrInvalid
	}
	root, err := s.open(true)
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	id, directory, identity, err := createEntryDirectory(root)
	if err != nil {
		return Entry{}, err
	}
	defer directory.Close()
	owned := make(map[string]os.FileInfo)
	complete := false
	defer func() {
		if !complete {
			err = errors.Join(err, cleanupDirectory(root, id, directory, identity, owned))
		}
	}()
	file, info, err := createPrivateFile(directory, "audio")
	if err != nil {
		return Entry{}, err
	}
	owned["audio"] = info
	digest := sha256.New()
	count, copyErr := copyContext(ctx, io.MultiWriter(file, digest), source)
	audioInfo, statErr := file.Stat()
	closeBodyErr := source.Close()
	syncErr := file.Sync()
	closeFileErr := file.Close()
	if err := errors.Join(copyErr, statErr, closeBodyErr, syncErr, closeFileErr, ctx.Err()); err != nil {
		return Entry{}, err
	}
	owned["audio"] = audioInfo
	entry = Entry{ID: id, CreatedAt: time.Now().UTC(), Settings: settings, Bytes: count, SHA256: hex.EncodeToString(digest.Sum(nil))}
	metadata, info, err := createPrivateFile(directory, ".manifest")
	if err != nil {
		return Entry{}, err
	}
	owned[".manifest"] = info
	encodeErr := json.NewEncoder(metadata).Encode(manifest{Version: 1, Entry: entry})
	metadataInfo, statErr := metadata.Stat()
	syncErr, closeFileErr = metadata.Sync(), metadata.Close()
	if err := errors.Join(encodeErr, statErr, syncErr, closeFileErr, ctx.Err()); err != nil {
		return Entry{}, err
	}
	owned[".manifest"] = metadataInfo
	if err := verifyDirectory(root, id, identity); err != nil {
		return Entry{}, err
	}
	for name, expected := range owned {
		if err := verifyUnchangedFile(directory, name, expected); err != nil {
			return Entry{}, err
		}
	}
	// The exclusive hard link is the commit point. Unlike rename, it cannot
	// overwrite an unexpected manifest. Pending directories have no manifest.
	if err := directory.Link(".manifest", "manifest.json"); err != nil {
		return Entry{}, err
	}
	owned["manifest.json"] = metadataInfo
	if err := errors.Join(removeOwned(directory, ".manifest", metadataInfo), ctx.Err()); err != nil {
		return Entry{}, err
	}
	delete(owned, ".manifest")
	if err := verifyDirectory(root, id, identity); err != nil {
		return Entry{}, err
	}
	complete = true
	return entry, nil
}

// List returns valid entries alongside any damaged-entry errors. Uncommitted
// directories and unrelated names remain invisible and untouched.
func (s Store) List(ctx context.Context) ([]Entry, error) {
	root, err := s.open(false)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var entries []Entry
	err = scanEntries(ctx, root, false, func(id string) error {
		directory, identity, err := openEntryDirectory(root, id)
		if errors.Is(err, os.ErrNotExist) {
			return nil // A concurrent clear removed the enumerated directory.
		}
		if err != nil {
			return err
		}
		defer directory.Close()
		entry, _, err := readManifest(ctx, directory, id)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := verifyDirectory(root, id, identity); err != nil {
			return err
		}
		audio, info, err := openPrivateFile(directory, "audio")
		if err != nil {
			return errors.Join(ErrChanged, err)
		}
		closeErr := audio.Close()
		if info.Size() != entry.Bytes {
			return errors.Join(ErrChanged, closeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		entries = append(entries, entry)
		return nil
	})
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	if entries == nil {
		entries = []Entry{}
	}
	return entries, err
}

func validSettings(settings Settings) bool {
	switch settings.Format {
	case "mp3", "opus", "aac", "flac", "wav", "pcm":
	default:
		return false
	}
	for _, value := range []string{settings.Input, settings.Instructions, settings.Model, settings.Voice} {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return !math.IsNaN(settings.Speed) && !math.IsInf(settings.Speed, 0) && settings.Speed >= 0
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, char := range id {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func privateMode(info os.FileInfo, directory bool) bool {
	if directory && !info.IsDir() || !directory && !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0
}

func (s Store) open(create bool) (*os.Root, error) {
	if s.Directory == "" {
		return nil, ErrInvalid
	}
	if create {
		if err := os.MkdirAll(s.Directory, 0700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(s.Directory)
	if err != nil {
		return nil, err
	}
	if !privateMode(info, true) {
		return nil, ErrInvalid
	}
	root, err := os.OpenRoot(s.Directory)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.Join(err, ErrChanged, root.Close())
	}
	return root, nil
}

func createEntryDirectory(root *os.Root) (string, *os.Root, os.FileInfo, error) {
	for {
		id, err := newID()
		if err != nil {
			return "", nil, nil, err
		}
		if err := root.Mkdir(id, 0700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", nil, nil, err
		}
		directory, identity, err := openEntryDirectory(root, id)
		return id, directory, identity, err
	}
}

func openEntryDirectory(root *os.Root, id string) (*os.Root, os.FileInfo, error) {
	info, err := root.Lstat(id)
	if err != nil {
		return nil, nil, err
	}
	if !privateMode(info, true) {
		return nil, nil, ErrInvalid
	}
	directory, err := root.OpenRoot(id)
	if err != nil {
		return nil, nil, err
	}
	actual, err := directory.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return nil, nil, errors.Join(err, ErrChanged, directory.Close())
	}
	return directory, info, nil
}

func createPrivateFile(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	file, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	return file, info, nil
}

func openPrivateFile(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !privateMode(info, false) {
		return nil, nil, ErrInvalid
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	actual, statErr := file.Stat()
	if statErr != nil || !privateMode(actual, false) || !os.SameFile(info, actual) {
		return nil, nil, errors.Join(statErr, ErrChanged, file.Close())
	}
	if err := verifyFile(root, name, actual); err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	return file, actual, nil
}

func verifyFile(root *os.Root, name string, expected os.FileInfo) error {
	info, err := root.Lstat(name)
	if err != nil || !privateMode(info, false) || !os.SameFile(info, expected) {
		return errors.Join(err, ErrChanged)
	}
	return nil
}

func verifyUnchangedFile(root *os.Root, name string, expected os.FileInfo) error {
	if err := verifyFile(root, name, expected); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil || !os.SameFile(info, expected) || info.Size() != expected.Size() || !info.ModTime().Equal(expected.ModTime()) {
		return errors.Join(err, ErrChanged)
	}
	return nil
}

func verifyDirectory(root *os.Root, id string, expected os.FileInfo) error {
	info, err := root.Lstat(id)
	if err != nil || !privateMode(info, true) || !os.SameFile(info, expected) {
		return errors.Join(err, ErrChanged)
	}
	return nil
}

func readManifest(ctx context.Context, root *os.Root, id string) (Entry, os.FileInfo, error) {
	return readManifestFile(ctx, root, "manifest.json", id)
}

func readManifestFile(ctx context.Context, root *os.Root, name, id string) (Entry, os.FileInfo, error) {
	file, identity, err := openPrivateFile(root, name)
	if err != nil {
		return Entry{}, nil, err
	}
	raw, readErr := io.ReadAll(contextReader{ctx, file})
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr, ctx.Err()); err != nil {
		return Entry{}, nil, err
	}
	if !utf8.Valid(raw) {
		return Entry{}, nil, ErrInvalid
	}
	decoder := json.NewDecoder(contextReader{ctx, bytes.NewReader(raw)})
	decoder.DisallowUnknownFields()
	var data manifest
	decodeErr := decoder.Decode(&data)
	if decodeErr == nil && decoder.Decode(new(any)) != io.EOF {
		decodeErr = ErrInvalid
	}
	if err := errors.Join(decodeErr, ctx.Err()); err != nil {
		return Entry{}, nil, errors.Join(ErrInvalid, err)
	}
	if !uniqueManifestKeys(json.NewDecoder(contextReader{ctx, bytes.NewReader(raw)})) {
		return Entry{}, nil, errors.Join(ErrInvalid, ctx.Err())
	}
	entry := data.Entry
	digest, err := hex.DecodeString(entry.SHA256)
	if data.Version != 1 || entry.ID != id || !validID(id) || entry.CreatedAt.IsZero() || entry.Bytes < 0 ||
		!validSettings(entry.Settings) || err != nil || len(digest) != sha256.Size || strings.ToLower(entry.SHA256) != entry.SHA256 {
		return Entry{}, nil, ErrInvalid
	}
	if err := verifyUnchangedFile(root, name, identity); err != nil {
		return Entry{}, nil, err
	}
	return entry, identity, nil
}

// The typed decode runs first, so these objects contain only the manifest's
// fixed, shallow schema. Reject duplicate keys instead of accepting last wins.
func uniqueManifestKeys(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	if token != json.Delim('{') {
		return true
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !uniqueManifestKeys(decoder) {
			return false
		}
		seen[key] = true
	}
	token, err = decoder.Token()
	return err == nil && token == json.Delim('}')
}

func scanEntries(ctx context.Context, root *os.Root, includeClearing bool, visit func(string) error) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	var failures error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		entries, err := file.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return errors.Join(failures, err)
			}
			id := entry.Name()
			if includeClearing {
				id = strings.TrimPrefix(id, clearPrefix)
			}
			if validID(id) {
				failures = errors.Join(failures, visit(id))
			}
		}
		if err == io.EOF {
			return failures
		}
		if err != nil {
			return errors.Join(failures, err)
		}
	}
}

func removeOwned(root *os.Root, name string, expected os.FileInfo) error {
	if err := verifyFile(root, name, expected); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return root.Remove(name)
}

func cleanupDirectory(root *os.Root, id string, directory *os.Root, identity os.FileInfo, owned map[string]os.FileInfo) error {
	if err := verifyDirectory(root, id, identity); err != nil {
		return err
	}
	var result error
	for name, info := range owned {
		result = errors.Join(result, removeOwned(directory, name, info))
	}
	if result != nil {
		return result
	}
	if err := verifyDirectory(root, id, identity); err != nil {
		return err
	}
	return root.Remove(id)
}

type ownedBody struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (b *ownedBody) Close() error {
	b.once.Do(func() { b.err = b.ReadCloser.Close() })
	return b.err
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.source.Read(buffer[:min(len(buffer), 32<<10)])
	if canceled := r.ctx.Err(); canceled != nil {
		return n, errors.Join(err, canceled)
	}
	return n, err
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	return io.CopyBuffer(destination, contextReader{ctx, source}, make([]byte, 32<<10))
}
