package cmd

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sync"
)

// fileUpload wraps an io.Reader with filename and content-type metadata for
// use as a multipart form part. The apiform encoder detects the Filename and
// ContentType methods and uses them to populate the Content-Disposition
// filename and the Content-Type header on the part.
type fileUpload struct {
	io.Reader   // apiform checks for reader and reads its contents during encode
	filename    string
	contentType string
	size        int64
	knownSize   bool
}

func (f fileUpload) Filename() string    { return f.filename }
func (f fileUpload) ContentType() string { return f.contentType }
func (f fileUpload) Close() error {
	if closer, ok := f.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (f fileUpload) hasKnownSize() bool {
	return f.knownSize
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

// openFileUpload opens path once. Ordinary files retain their exact stat size
// so the request can stream with Content-Length. Virtual and unknown-length
// files remain single-shot streams with chunked transfer encoding.
func openFileUpload(path string) (fileUpload, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileUpload{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return fileUpload{}, errors.Join(err, file.Close())
	}
	if info.IsDir() {
		return fileUpload{}, errors.Join(fmt.Errorf("read %s: is a directory", path), file.Close())
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if !hasTrustworthyFileSize(file, info) {
		return fileUpload{
			Reader:      file,
			filename:    filepath.Base(path),
			contentType: contentType,
		}, nil
	}

	return fileUpload{
		Reader: &exactLengthReadCloser{
			exactLengthReader: exactLengthReader{
				reader:    io.NewSectionReader(file, 0, info.Size()),
				remaining: info.Size(),
			},
			closer: file,
		},
		filename:    filepath.Base(path),
		contentType: contentType,
		size:        info.Size(),
		knownSize:   true,
	}, nil
}
