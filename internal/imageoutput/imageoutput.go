// Package imageoutput saves base64 Images API responses to local image files.
package imageoutput

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ResolveDirectory returns an absolute, writable output directory. The default
// directory is created when necessary; a caller-selected directory must exist.
func ResolveDirectory(requested string) (string, error) {
	useDefault := requested == ""
	if useDefault || requested == "~" || strings.HasPrefix(requested, "~/") || strings.HasPrefix(requested, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve image output home directory: %w", err)
		}
		if useDefault {
			requested = filepath.Join(home, "Downloads", "gpt-images")
		} else if requested == "~" {
			requested = home
		} else {
			requested = filepath.Join(home, requested[2:])
		}
	}
	directory, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolve image output directory: %w", err)
	}
	if useDefault {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return "", fmt.Errorf("create default image output directory: %w", err)
		}
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("open image output directory (choose an existing directory): %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("image output path is not a directory: %s", directory)
	}
	// Check actual write access before the caller starts an image request; mode
	// bits alone do not account for ACLs or a read-only filesystem.
	probe, err := os.CreateTemp(directory, ".gpt-image-write-check-*")
	if err != nil {
		return "", fmt.Errorf("image output directory is not writable: %w", err)
	}
	closeErr := probe.Close()
	removeErr := os.Remove(probe.Name())
	if err := errors.Join(closeErr, removeErr); err != nil {
		return "", fmt.Errorf("check image output directory write access: %w", err)
	}
	return directory, nil
}

// ValidateName checks a filename stem that can be used on supported platforms.
// The image's actual format supplies its extension; paths are not accepted.
func ValidateName(name string) error {
	if name == "" || strings.TrimSpace(name) == "" {
		return errors.New("image name must not be empty")
	}
	if !utf8.ValidString(name) || strings.ContainsAny(name, `/\\<>:"|?*`) || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("image name must be a filename, without path separators, control characters, or <>:\"|?*")
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return errors.New("image name must not end with a space or period")
	}
	// Windows device names stay reserved when followed by an extension. The
	// superscript digits are also recognized as COM/LPT device numbers.
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return errors.New("image name is reserved by the operating system")
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		if suffix := []rune(base[3:]); len(suffix) == 1 && strings.ContainsRune("123456789¹²³", suffix[0]) {
			return errors.New("image name is reserved by the operating system")
		}
	}
	return nil
}

// SaveResponse saves every base64 image in an Images API JSON response. It
// returns absolute paths in response order and removes its files if any image
// fails to save. URL responses are not downloaded. An omitted or empty name
// uses the local date and time. Existing filenames receive -2, -3, etc.
func SaveResponse(ctx context.Context, raw []byte, directory string, name ...string) (paths []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stem := "image-" + time.Now().Format("2006-01-02-150405")
	if len(name) > 1 {
		return nil, errors.New("provide only one image name")
	}
	if len(name) == 1 && name[0] != "" {
		if err := ValidateName(name[0]); err != nil {
			return nil, err
		}
		stem = name[0]
	}
	var response struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("parse image response: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(response.Data) == 0 {
		return nil, errors.New("image response contains no images")
	}
	directory, err = ResolveDirectory(directory)
	if err != nil {
		return nil, err
	}
	var created []string
	defer func() {
		if err != nil {
			for _, path := range created {
				if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = errors.Join(err, fmt.Errorf("remove incomplete image output: %w", removeErr))
				}
			}
		}
	}()
	for i, item := range response.Data {
		if item.Base64 == "" {
			return nil, fmt.Errorf("image %d has no base64 image data; URL responses cannot be saved", i+1)
		}
		path, saveErr := saveImage(ctx, item.Base64, directory, stem)
		if saveErr != nil {
			return nil, fmt.Errorf("save image %d: %w", i+1, saveErr)
		}
		created = append(created, path)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return created, nil
}

func saveImage(ctx context.Context, encoded, directory, stem string) (path string, err error) {
	reader := contextReader{ctx, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded))}
	var header [16]byte
	n, readErr := io.ReadFull(reader, header[:])
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("decode base64 image: %w", readErr)
	}
	extension := imageExtension(header[:n])
	if extension == "" {
		return "", errors.New("unsupported image data; expected PNG, JPEG, or WebP")
	}
	file, err := createImageFile(ctx, directory, stem, extension)
	if err != nil {
		return "", fmt.Errorf("create image file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close image file: %w", closeErr))
		}
		if err != nil {
			if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove incomplete image file: %w", removeErr))
			}
			path = ""
		}
	}()
	if _, err := file.Write(header[:n]); err != nil {
		return "", fmt.Errorf("write image file: %w", err)
	}
	if _, err := io.Copy(file, reader); err != nil {
		return "", fmt.Errorf("decode or write image file: %w", err)
	}
	return file.Name(), ctx.Err()
}

func createImageFile(ctx context.Context, directory, stem, extension string) (*os.File, error) {
	for sequence := 1; ; sequence++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := stem
		if sequence > 1 {
			name = fmt.Sprintf("%s-%d", stem, sequence)
		}
		// Exclusive creation handles concurrent generations atomically and never
		// follows an existing symlink or overwrites a user's file.
		file, err := os.OpenFile(filepath.Join(directory, name+extension), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return file, err
	}
}

// Inspect only container signatures, without allocating a decoded image or
// trusting the response's requested output format to choose the extension.
func imageExtension(header []byte) string {
	switch {
	case bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")):
		return ".png"
	case bytes.HasPrefix(header, []byte{0xff, 0xd8, 0xff}):
		return ".jpeg"
	case len(header) >= 16 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		switch string(header[12:16]) {
		case "VP8 ", "VP8L", "VP8X":
			return ".webp"
		}
	}
	return ""
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
