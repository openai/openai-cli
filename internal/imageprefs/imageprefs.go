// Package imageprefs stores only the automatic image-preview preference.
// It never reads or rewrites the CLI's other configuration or preview cache.
package imageprefs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type preferences struct {
	Version int   `json:"version"`
	Inline  *bool `json:"inline"`
}

// Load returns auto when no preference was saved. Unknown or corrupt settings
// are errors, never implicit permission to activate a terminal image font.
func Load(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, name, err := openParent(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return "auto", nil
	}
	if err != nil {
		return "", err
	}
	defer root.Close()
	return load(ctx, root, name)
}

var errChanged = errors.New("image preferences changed while opening")

func load(ctx context.Context, root *os.Root, name string) (string, error) {
	for range 64 {
		mode, err := loadSnapshot(ctx, root, name)
		if !errors.Is(err, errChanged) {
			return mode, err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	return "", errChanged
}

func loadSnapshot(ctx context.Context, root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "auto", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("image preferences must be a regular file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return "", errChanged
	}
	// This bound applies only to a tiny local settings file, never API data.
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var prefs preferences
	if len(data) > 4096 || !decodePreferences(data, &prefs) {
		return "", errors.New("unknown or invalid image preferences; existing settings were kept")
	}
	if *prefs.Inline {
		return "on", nil
	}
	return "off", nil
}

// Save replaces this single setting atomically. Concurrent on/off commands have
// last-completed-write semantics, and readers never observe a partial JSON file.
// Unknown schemas and unrelated fields are preserved by refusing to overwrite.
func Save(ctx context.Context, path string, inline bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, name, err := openParent(path, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := load(ctx, root, name); err != nil {
		return err
	}
	data, err := json.Marshal(preferences{Version: 1, Inline: &inline})
	if err != nil {
		return err
	}
	temporary := ".image-preferences-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return err
	}
	// Rename replaces an entry, never the target of a symlink installed meanwhile.
	return root.Rename(temporary, name)
}

func openParent(path string, create bool) (*os.Root, string, error) {
	directory, name := filepath.Dir(path), filepath.Base(path)
	if create {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, "", err
		}
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("image preferences require a regular configuration directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		if err == nil {
			err = errors.New("configuration directory changed while opening")
		}
		return nil, "", err
	}
	return root, name, nil
}

// Duplicate keys are ambiguous for a setting that permits font activation.
func decodePreferences(data []byte, prefs *preferences) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		switch key {
		case "version":
			err = decoder.Decode(&prefs.Version)
		case "inline":
			err = decoder.Decode(&prefs.Inline)
		default:
			return false
		}
		if err != nil {
			return false
		}
	}
	token, err = decoder.Token()
	return err == nil && token == json.Delim('}') && prefs.Version == 1 && prefs.Inline != nil && decoder.Decode(new(any)) == io.EOF
}
