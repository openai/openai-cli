package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	_ "golang.org/x/image/webp"
)

// ReadSaved decodes an optional preview. These limits protect terminal rendering;
// they never limit the API response or change the original saved image.
func ReadSaved(ctx context.Context, path string) (image.Image, error) {
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
	return readSavedFile(ctx, file)
}

// readSavedFile validates and decodes one bounded snapshot. Its caller owns file.
func readSavedFile(ctx context.Context, file *os.File) (image.Image, error) {
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
	data, err := io.ReadAll(contextReader{ctx, io.LimitReader(file, (64<<20)+1)})
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<20 {
		return nil, errors.New("image exceeds the 64 MiB preview limit")
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
	n, err := r.reader.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, errors.Join(err, canceled)
	}
	return n, err
}
