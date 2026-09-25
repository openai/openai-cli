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
)

// Decode items independently, without retaining another raw copy of large
// base64 payloads, so malformed siblings do not prevent saving valid images.
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

// JSON errors may include response scalar values. Retain the cause for callers
// without allowing sensitive image response data into the diagnostic string.
type responseJSONError struct{ cause error }

func (e responseJSONError) Error() string { return "invalid image response JSON" }
func (e responseJSONError) Unwrap() error { return e.cause }

// SaveResponse saves every base64 image, returning completed absolute paths in
// response order even if siblings fail. Only incomplete files are removed.
// Cancellation stops the batch. URL responses are not downloaded. An omitted
// name uses local date/time; collisions receive -2, -3, and subsequent suffixes.
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
		return nil, fmt.Errorf("parse image response: %w", responseJSONError{err})
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
			failures = append(failures, fmt.Errorf("read image %d: %w", i+1, responseJSONError{item.err}))
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
		info, statErr := file.Stat()
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, imagePathError("close image file", file.Name(), closeErr))
		}
		if err != nil {
			if statErr != nil {
				err = errors.Join(err, imagePathError("inspect incomplete image file", file.Name(), statErr))
			} else if removeErr := removeOwnedFile(file.Name(), info); removeErr != nil {
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
		// Exclusive creation handles concurrent saves atomically and never
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

// Container signatures choose extensions without decoding pixels or trusting a
// requested output format. This does not validate the entire image file.
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
