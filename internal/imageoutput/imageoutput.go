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
		return "", imagePathError("resolve image output directory", requested, err)
	}
	if useDefault {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return "", imagePathError("create default image output directory", directory, err)
		}
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", imagePathError("open image output directory (choose an existing directory)", directory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("image output path is not a directory: %q", directory)
	}
	// Check actual write access before the caller starts an image request; mode
	// bits alone do not account for ACLs or a read-only filesystem.
	probe, err := os.CreateTemp(directory, ".gpt-image-write-check-*")
	if err != nil {
		return "", imagePathError("image output directory is not writable", directory, err)
	}
	if err := removeProbe(probe); err != nil {
		return "", fmt.Errorf("check image output directory write access: %w", err)
	}
	return directory, nil
}

// NormalizeName accepts a familiar image filename while keeping the actual
// response format authoritative. Strip one image extension, never path parts.
func NormalizeName(name string) (string, error) {
	extension := filepath.Ext(name)
	switch strings.ToLower(extension) {
	case ".png", ".jpeg", ".jpg", ".webp":
		name = strings.TrimSuffix(name, extension)
	}
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

// CheckName checks the filesystem's real filename constraints before the paid
// request. The directory has already been resolved; an empty name uses the
// short default timestamp. The probe is exclusive and only its own file is
// removed. Use the longest supported extension to cover all image formats.
func CheckName(ctx context.Context, directory, normalizedName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if normalizedName == "" {
		return nil
	}
	if err := ValidateName(normalizedName); err != nil {
		return err
	}
	probe, err := createImageFile(ctx, directory, normalizedName, ".jpeg")
	if err != nil {
		return fmt.Errorf("cannot use this image name in the output folder; try a shorter name or another folder: %w", err)
	}
	err = removeProbe(probe)
	return errors.Join(err, ctx.Err())
}

func removeProbe(probe *os.File) error {
	var failures []error
	if err := probe.Close(); err != nil {
		failures = append(failures, imagePathError("close image write check", probe.Name(), err))
	}
	if err := os.Remove(probe.Name()); err != nil {
		failures = append(failures, imagePathError("remove image write check", probe.Name(), err))
	}
	return errors.Join(failures...)
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

// Keep a malformed item's error local so valid siblings can still be saved.
// Decode directly from the response slice instead of retaining a RawMessage
// copy of each potentially large base64 payload.
type responseImageItem struct {
	Base64 string
	err    error
}

func (item *responseImageItem) UnmarshalJSON(raw []byte) error {
	var decoded struct {
		Base64 string `json:"b64_json"`
	}
	item.err = json.Unmarshal(raw, &decoded)
	item.Base64 = decoded.Base64
	return nil
}

// SaveResponse saves every base64 image in an Images API JSON response. It
// returns absolute paths in response order, including completed images when
// another image fails. Only incomplete files are removed. Invalid individual
// images do not prevent later valid images being saved; cancellation stops the
// batch. Errors include the saved/total count. URL responses are not downloaded.
// An omitted or empty name uses the local date and time. Existing filenames
// receive -2, -3, etc.
func SaveResponse(ctx context.Context, raw []byte, directory string, name ...string) (paths []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stem := "image-" + time.Now().Format("2006-01-02-150405")
	if len(name) > 1 {
		return nil, errors.New("provide only one image name")
	}
	if len(name) == 1 && name[0] != "" {
		normalized, err := NormalizeName(name[0])
		if err != nil {
			return nil, err
		}
		stem = normalized
	}
	var response struct {
		Data []responseImageItem `json:"data"`
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
	defer func() {
		if err != nil {
			err = fmt.Errorf("saved %d of %d images; could not finish saving: %w", len(paths), len(response.Data), err)
		}
	}()
	directory, err = ResolveDirectory(directory)
	if err != nil {
		return nil, err
	}
	var failures []error
	for i, item := range response.Data {
		if err := ctx.Err(); err != nil {
			return paths, errors.Join(append(failures, err)...)
		}
		if item.err != nil {
			failures = append(failures, fmt.Errorf("read image %d: %w", i+1, item.err))
			continue
		}
		if item.Base64 == "" {
			failures = append(failures, fmt.Errorf("image %d has no base64 image data; URL responses cannot be saved", i+1))
			continue
		}
		path, saveErr := saveImage(ctx, item.Base64, directory, stem)
		if saveErr != nil {
			failures = append(failures, fmt.Errorf("save image %d: %w", i+1, saveErr))
			if errors.Is(saveErr, context.Canceled) || errors.Is(saveErr, context.DeadlineExceeded) {
				return paths, errors.Join(failures...)
			}
			continue
		}
		paths = append(paths, path)
	}
	return paths, errors.Join(failures...)
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
		return "", err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, imagePathError("close image file", file.Name(), closeErr))
		}
		if err != nil {
			if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, imagePathError("remove incomplete image file", file.Name(), removeErr))
			}
			path = ""
		}
	}()
	if _, err := file.Write(header[:n]); err != nil {
		return "", imagePathError("write image file", file.Name(), err)
	}
	if _, err := io.Copy(file, reader); err != nil {
		return "", imagePathError("decode or write image file", file.Name(), err)
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
		path := filepath.Join(directory, name+extension)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, imagePathError("create image file", path, err)
		}
		return file, nil
	}
}

func imagePathError(operation, path string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
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
