// Package imageprefs stores the user's default for automatic image previews.
// It is independent of preview caches, so clearing previews keeps this setting.
package imageprefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type preferences struct {
	Version int   `json:"version"`
	Inline  *bool `json:"inline"`
}

// Load defaults to on when no preference file exists. Malformed files are
// reported without echoing their contents; Save can replace them to recover.
func Load(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("image preferences must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	// This is a tiny local settings file, never an API response or image.
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return false, err
	}
	var prefs preferences
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 4096 || decoder.Decode(&prefs) != nil || prefs.Version != 1 || prefs.Inline == nil || decoder.Decode(new(any)) != io.EOF {
		return false, errors.New("invalid image preferences; run openai images inline on or openai images inline off to replace them")
	}
	return *prefs.Inline, nil
}

// Save atomically replaces a preference without following a file symlink.
// Newly created configuration directories and files are private to the user.
func Save(path string, inline bool) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("image preferences require a regular configuration directory")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("image preferences must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(preferences{Version: 1, Inline: &inline})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".image-preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("save image preferences: %w", err)
	}
	return nil
}
