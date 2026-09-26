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
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return nil, errors.New("image is not a regular file or exceeds the 64 MiB preview limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(contextReader{ctx, io.LimitReader(file, 64<<20)})
	if err != nil {
		return nil, err
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
