package custom

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf8"
)

// This bound applies only to optional local persistence, never image requests.
const imagePickerStateLimit = 1 << 20

var (
	errImagePickerStateInvalid = errors.New("saved image picker settings are unknown or invalid")
	errImagePickerStateChanged = errors.New("saved image picker settings changed while opening")
)

// Keep this record separate from preview preferences and root configuration.
// In particular, it cannot contain connection settings or credentials.
type imagePickerState struct {
	Version    int    `json:"version"`
	Prompt     string `json:"prompt"`
	Model      string `json:"model"`
	Size       string `json:"size"`
	Quality    string `json:"quality"`
	Background string `json:"background"`
	Format     string `json:"format"`
	Count      string `json:"count"`
	OutputDir  string `json:"output_dir"`
}

func imagePickerStatePath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "openai", "image-picker.json"), nil
}

func loadImagePickerState(ctx context.Context, path string) (imagePickerSettings, bool, error) {
	if err := ctx.Err(); err != nil {
		return imagePickerSettings{}, false, err
	}
	root, name, err := openImagePickerStateParent(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return imagePickerSettings{}, false, nil
	}
	if err != nil {
		return imagePickerSettings{}, false, err
	}
	defer root.Close()
	return readImagePickerState(ctx, root, name)
}

func readImagePickerState(ctx context.Context, root *os.Root, name string) (imagePickerSettings, bool, error) {
	for range 64 {
		if err := ctx.Err(); err != nil {
			return imagePickerSettings{}, false, err
		}
		settings, found, err := readImagePickerStateSnapshot(ctx, root, name)
		if !errors.Is(err, errImagePickerStateChanged) {
			return settings, found, err
		}
	}
	return imagePickerSettings{}, false, errImagePickerStateChanged
}

func readImagePickerStateSnapshot(ctx context.Context, root *os.Root, name string) (imagePickerSettings, bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return imagePickerSettings{}, false, nil
	}
	if err != nil {
		return imagePickerSettings{}, false, err
	}
	if !privateImagePickerState(info) {
		return imagePickerSettings{}, false, errors.New("saved image picker settings must be a private regular file")
	}
	// Nonblocking open prevents a replaced FIFO from waiting for a writer.
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return imagePickerSettings{}, false, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return imagePickerSettings{}, false, err
	}
	if !privateImagePickerState(actual) || !os.SameFile(info, actual) {
		return imagePickerSettings{}, false, errImagePickerStateChanged
	}
	current, err := root.Lstat(name)
	if err != nil || !privateImagePickerState(current) || !os.SameFile(actual, current) {
		return imagePickerSettings{}, false, errImagePickerStateChanged
	}
	if actual.Size() > imagePickerStateLimit {
		return imagePickerSettings{}, false, errImagePickerStateInvalid
	}
	data, err := io.ReadAll(io.LimitReader(imagePickerStateReader{ctx, file}, imagePickerStateLimit+1))
	if err != nil {
		return imagePickerSettings{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return imagePickerSettings{}, false, err
	}
	// Validate and decode the same bounded snapshot, including in-place rewrites.
	settings, valid := decodeImagePickerState(data)
	if err := ctx.Err(); err != nil {
		return imagePickerSettings{}, false, err
	}
	if !valid || len(data) > imagePickerStateLimit {
		return imagePickerSettings{}, false, errImagePickerStateInvalid
	}
	return settings, true, nil
}

// Save replaces a whole record. Concurrent picker saves may win in either order;
// readers see only complete records, and there is no field merging. Files from
// unknown schemas or corrupt existing records are never intentionally replaced.
func saveImagePickerState(ctx context.Context, path string, settings imagePickerSettings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validImagePickerState(settings) {
		return errImagePickerStateInvalid
	}
	state := imagePickerState{1, settings.prompt, settings.model, settings.size, settings.quality, settings.background, settings.format, settings.count, settings.outputDir}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data)+1 > imagePickerStateLimit {
		return errors.New("image picker settings exceed the local persistence limit")
	}
	root, name, err := openImagePickerStateParent(path, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, _, err := readImagePickerState(ctx, root, name); err != nil {
		return err
	}
	temporary := ".image-picker-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := io.Copy(file, imagePickerStateReader{ctx, bytes.NewReader(append(data, '\n'))})
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return err
	}
	// Recheck after writing so a newly invalid record also remains untouched.
	if _, _, err := readImagePickerState(ctx, root, name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}

func openImagePickerStateParent(path string, create bool) (*os.Root, string, error) {
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
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0) {
		return nil, "", errors.New("image picker settings require a configuration directory writable only by its owner")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		if err == nil {
			err = errImagePickerStateChanged
		}
		return nil, "", err
	}
	return root, name, nil
}

func privateImagePickerState(info os.FileInfo) bool {
	return info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0)
}

func decodeImagePickerState(data []byte) (imagePickerSettings, bool) {
	s := imagePickerSettings{}
	if !utf8.Valid(data) {
		return s, false
	}
	fields := map[string]*string{"prompt": &s.prompt, "model": &s.model, "size": &s.size, "quality": &s.quality, "background": &s.background, "format": &s.format, "count": &s.count, "output_dir": &s.outputDir}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return s, false
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return s, false
		}
		seen[key] = true
		value, err := decoder.Token()
		if err != nil {
			return s, false
		}
		if key == "version" {
			if value != json.Number("1") {
				return s, false
			}
		} else {
			text, ok := value.(string)
			field, known := fields[key]
			if !ok || !known {
				return s, false
			}
			*field = text
		}
	}
	token, err = decoder.Token()
	return s, err == nil && token == json.Delim('}') && len(seen) == len(fields)+1 && decoder.Decode(new(any)) == io.EOF && validImagePickerState(s)
}

func validImagePickerState(settings imagePickerSettings) bool {
	if settings.outputDir != "" && (!filepath.IsAbs(settings.outputDir) || strings.ContainsRune(settings.outputDir, 0)) {
		return false
	}
	total := 0
	for _, value := range []string{settings.prompt, settings.model, settings.size, settings.quality, settings.background, settings.format, settings.count, settings.outputDir} {
		if !utf8.ValidString(value) || len(value) > imagePickerStateLimit-total {
			return false
		}
		total += len(value)
	}
	m := imagePicker{settings: settings}
	for _, field := range []string{"model", "size", "quality", "background", "format", "count"} {
		found := false
		for _, choice := range m.choices(field) {
			found = found || choice.value == m.value(field)
		}
		if !found {
			return false
		}
	}
	return settings.background != "transparent" || settings.format != "jpeg"
}

type imagePickerStateReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r imagePickerStateReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data[:min(len(data), 32<<10)])
}
