package terminalimage

import (
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

// readSavedFile validates and decodes the same descriptor. Its caller owns file.
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
	config, format, err := image.DecodeConfig(contextReader{ctx, io.LimitReader(file, 64<<20)})
	if err != nil {
		return nil, err
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return nil, image.ErrFormat
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return nil, errors.New("image exceeds the 16 megapixel preview limit")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	result, _, err := image.Decode(contextReader{ctx, io.LimitReader(file, 64<<20)})
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
	return n, errors.Join(err, r.ctx.Err())
}
