package terminalimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	_ "golang.org/x/image/webp"
)

// ErrSavedImageChanged means the preview bytes differ from the completed save.
var ErrSavedImageChanged = errors.New("saved image contents changed before preview")

// ReadSaved decodes an optional preview. These limits protect terminal rendering;
// they never limit the API response or change the original saved image.
func ReadSaved(ctx context.Context, path string) (image.Image, error) {
	return readSaved(ctx, path, nil)
}

// ReadSavedMatching previews only the response bytes recorded by a prior save.
// Both identity checks and decoding apply to the same bounded snapshot, so a
// replaced path or a rewritten open file cannot substitute another image.
func ReadSavedMatching(ctx context.Context, path string, expected [sha256.Size]byte) (image.Image, error) {
	return readSaved(ctx, path, &expected)
}

func readSaved(ctx context.Context, path string, expected *[sha256.Size]byte) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A pathname can change from a regular image to a FIFO before it is opened.
	// Open without waiting for a writer, then check the descriptor we will read.
	file, err := openSavedImage(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readSavedFile(ctx, file, expected)
}

// readSavedFile validates and decodes one bounded snapshot. Its caller owns file.
func readSavedFile(ctx context.Context, file *os.File, expected *[sha256.Size]byte) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return nil, errors.New("image is not a regular file or exceeds the 64 MiB preview limit")
	}
	// Another process can rewrite even an already-open regular file. Inspect and
	// decode the same bytes so the dimension check applies to the decoded image.
	reader := io.LimitReader(file, (64<<20)+1)
	digest := sha256.New()
	if expected != nil {
		reader = io.TeeReader(reader, digest)
	}
	data, err := io.ReadAll(contextReader{ctx, reader})
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<20 {
		return nil, errors.New("image exceeds the 64 MiB preview limit")
	}
	if expected != nil && !bytes.Equal(digest.Sum(nil), expected[:]) {
		return nil, ErrSavedImageChanged
	}
	config, format, err := image.DecodeConfig(contextReader{ctx, bytes.NewReader(data)})
	if err != nil {
		return nil, err
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return nil, image.ErrFormat
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return nil, errors.New("image exceeds the 16 megapixel preview limit")
	}
	result, _, err := image.Decode(contextReader{ctx, bytes.NewReader(data)})
	if err != nil {
		return nil, err
	}
	return result, ctx.Err()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	// io.ReadAll grows its read buffer. Bound each read so snapshot hashing and
	// image decoding recheck cancellation after at most 32 KiB of input.
	n, err := r.reader.Read(p[:min(len(p), 32<<10)])
	if canceled := r.ctx.Err(); canceled != nil {
		return n, errors.Join(err, canceled)
	}
	return n, err
}
