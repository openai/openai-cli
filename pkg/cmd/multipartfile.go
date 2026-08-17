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
	source      *replayableFileSource
	ownsSource  bool
}

func (f fileUpload) Filename() string    { return f.filename }
func (f fileUpload) ContentType() string { return f.contentType }
func (f fileUpload) Close() error {
	if f.ownsSource && f.source != nil {
		return f.source.Close()
	}
	if closer, ok := f.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (f fileUpload) canReplay() bool {
	return f.source != nil && f.knownSize
}

func (f fileUpload) replay() (fileUpload, error) {
	if !f.canReplay() {
		return fileUpload{}, errors.New("multipart upload is not replayable")
	}
	reader, err := f.source.ReplayReader()
	if err != nil {
		return fileUpload{}, err
	}
	f.Reader = reader
	f.ownsSource = false
	return f, nil
}

// replayableFileSource owns an immutable, bounded on-disk snapshot. Each body
// reads from a duplicate handle, so redirects and retries always send the same
// bytes and closing the anchor cannot truncate a transport-owned upload.
type replayableFileSource struct {
	file       *os.File
	size       int64
	sourceName string

	closeOnce sync.Once
	closeErr  error
}

func (s *replayableFileSource) ReplayReader() (io.ReadCloser, error) {
	info, err := s.file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != s.size {
		return nil, fmt.Errorf("upload snapshot for %q changed while being replayed", s.displayName())
	}
	file, err := duplicateFile(s.file)
	if err != nil {
		return nil, fmt.Errorf("duplicate upload snapshot for %q: %w", s.displayName(), err)
	}
	return &exactLengthReadCloser{
		exactLengthReader: exactLengthReader{
			reader:    io.NewSectionReader(file, 0, s.size),
			remaining: s.size,
		},
		closer: file,
	}, nil
}

func (s *replayableFileSource) displayName() string {
	if s.sourceName != "" {
		return s.sourceName
	}
	return s.file.Name()
}

func (s *replayableFileSource) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.file.Close()
	})
	return s.closeErr
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
	closer io.Closer
}

func (r *exactLengthReadCloser) Close() error {
	return r.closer.Close()
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

// openFileUpload opens path once. Ordinary files are copied into a secure,
// size-bounded temporary snapshot so multipart redirects and retries are
// replayable without retaining the caller's mutable file or buffering it in
// memory. Virtual and unknown-length files remain single-shot streams.
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

	source, err := snapshotReplayableFile(file, info.Size(), path)
	if err != nil {
		return fileUpload{}, err
	}
	return fileUpload{
		Reader: &exactLengthReader{
			reader:    io.NewSectionReader(source.file, 0, source.size),
			remaining: source.size,
		},
		filename:    filepath.Base(path),
		contentType: contentType,
		size:        source.size,
		knownSize:   true,
		source:      source,
		ownsSource:  true,
	}, nil
}

func snapshotReplayableFile(source *os.File, size int64, sourceName string) (*replayableFileSource, error) {
	snapshot, err := createReplaySnapshotFile()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("snapshot upload %q: %w", sourceName, err), source.Close())
	}
	fail := func(cause error) (*replayableFileSource, error) {
		return nil, errors.Join(cause, source.Close(), snapshot.Close())
	}

	exactSource := &exactLengthReader{reader: source, remaining: size}
	if _, err := io.Copy(snapshot, exactSource); err != nil {
		return fail(fmt.Errorf("snapshot upload %q: %w", sourceName, err))
	}
	var extra [1]byte
	n, readErr := source.Read(extra[:])
	if n != 0 || !errors.Is(readErr, io.EOF) {
		if readErr == nil {
			readErr = errors.New("source grew while being snapshotted")
		}
		return fail(fmt.Errorf("snapshot upload %q: %w", sourceName, readErr))
	}
	if err := source.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close upload %q after snapshot: %w", sourceName, err), snapshot.Close())
	}
	return &replayableFileSource{
		file:       snapshot,
		size:       size,
		sourceName: sourceName,
	}, nil
}
