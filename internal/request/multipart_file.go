package request

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sync"

	"github.com/openai/openai-cli/internal/binaryparam"
)

// FileUpload wraps an io.Reader with filename and content-type metadata for
// use as a multipart form part. The apiform encoder detects the Filename and
// ContentType methods and uses them to populate the Content-Disposition
// filename and the Content-Type header on the part. The upload owns its Reader;
// KnownSize distinguishes an exact byte count from an unknown-length source.
type FileUpload struct {
	io.Reader // apiform checks for reader and reads its contents during encode
	FileName  string
	MediaType string
	Size      int64
	KnownSize bool
}

func (f FileUpload) Filename() string    { return f.FileName }
func (f FileUpload) ContentType() string { return f.MediaType }
func (f FileUpload) Close() error {
	if closer, ok := f.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type exactLengthReader struct {
	reader    io.Reader
	remaining int64
}

func (r *exactLengthReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	if errors.Is(err, io.EOF) {
		if r.remaining > 0 {
			return n, io.ErrUnexpectedEOF
		}
		return n, nil
	}
	return n, err
}

type exactLengthReadCloser struct {
	exactLengthReader
	closer    io.Closer
	closeOnce sync.Once
	closeErr  error
}

func (r *exactLengthReadCloser) Read(p []byte) (int, error) {
	n, err := r.exactLengthReader.Read(p)
	if r.remaining == 0 || err != nil {
		_ = r.Close()
	}
	return n, err
}

func (r *exactLengthReadCloser) Close() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.closer.Close()
	})
	return r.closeErr
}

func hasTrustworthyFileSize(file *os.File, info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return false
	}

	// Regular-looking virtual files can report a size unrelated to their
	// readable contents. Verify both sides of the reported EOF without moving
	// the descriptor offset; ordinary and sparse files support these ReadAt
	// probes, while procfs/sysfs-style sources fail or expose extra bytes.
	var probe [1]byte
	if info.Size() == 0 {
		n, err := file.ReadAt(probe[:], 0)
		return n == 0 && errors.Is(err, io.EOF)
	}
	n, err := file.ReadAt(probe[:], info.Size()-1)
	if n != 1 || err != nil {
		return false
	}
	n, err = file.ReadAt(probe[:], info.Size())
	return n == 0 && errors.Is(err, io.EOF)
}

// OpenFileUpload opens path once. Ordinary files retain their exact stat size
// so the request can stream with Content-Length. Virtual and unknown-length
// files remain single-shot streams with chunked transfer encoding.
func OpenFileUpload(path string) (FileUpload, error) {
	file, err := os.Open(path)
	if err != nil {
		return FileUpload{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return FileUpload{}, errors.Join(err, file.Close())
	}
	if info.IsDir() {
		return FileUpload{}, errors.Join(fmt.Errorf("read %s: is a directory", path), file.Close())
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if !hasTrustworthyFileSize(file, info) {
		reader, err := binaryparam.CancellableFile(file, info)
		if err != nil {
			return FileUpload{}, errors.Join(err, file.Close())
		}
		return FileUpload{
			Reader:    reader,
			FileName:  filepath.Base(path),
			MediaType: contentType,
		}, nil
	}

	return FileUpload{
		Reader: &exactLengthReadCloser{
			exactLengthReader: exactLengthReader{
				reader:    io.NewSectionReader(file, 0, info.Size()),
				remaining: info.Size(),
			},
			closer: file,
		},
		FileName:  filepath.Base(path),
		MediaType: contentType,
		Size:      info.Size(),
		KnownSize: true,
	}, nil
}
