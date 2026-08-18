package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// multipartReplaySource opportunistically captures the exact encoded bytes of
// the first request body. Capture failures never fail that first stream; they
// only make redirects and retries unavailable.
type multipartReplaySource struct {
	mu       sync.Mutex
	expected int64
	captured int64
	file     *os.File
	ready    bool
	closed   bool
	err      error
	closeErr error
}

func newMultipartReplaySource(expected int64) *multipartReplaySource {
	return &multipartReplaySource{expected: expected}
}

func (s *multipartReplaySource) Capture(p []byte) {
	if len(p) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready || s.closed || s.err != nil {
		return
	}
	if s.file == nil {
		file, err := createReplaySnapshotFile()
		if err != nil {
			s.failLocked(fmt.Errorf("create multipart replay snapshot: %w", err))
			return
		}
		s.file = file
	}

	n, err := s.file.Write(p)
	s.captured += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		s.failLocked(fmt.Errorf("write multipart replay snapshot: %w", err))
		return
	}
	if s.captured > s.expected {
		s.failLocked(fmt.Errorf("multipart replay snapshot exceeded expected length %d", s.expected))
		return
	}
	if s.captured == s.expected {
		s.ready = true
	}
}

func (s *multipartReplaySource) Fail(err error) {
	if err == nil {
		err = errors.New("multipart replay capture did not complete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		s.failLocked(err)
	}
}

func (s *multipartReplaySource) failLocked(err error) {
	if s.err == nil {
		s.err = err
	}
	if s.file != nil {
		s.closeErr = errors.Join(s.closeErr, s.file.Close())
		s.file = nil
	}
}

func (s *multipartReplaySource) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready && !s.closed
}

func (s *multipartReplaySource) ReplayReader() (io.ReadCloser, error) {
	s.mu.Lock()
	if !s.ready || s.closed || s.file == nil {
		err := s.err
		if err == nil {
			err = errors.New("first multipart stream did not complete")
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("multipart upload is not replayable: %w", err)
	}
	file, err := duplicateFile(s.file)
	s.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("duplicate multipart replay snapshot: %w", err)
	}
	return &exactLengthReadCloser{
		exactLengthReader: exactLengthReader{
			reader:    io.NewSectionReader(file, 0, s.expected),
			remaining: s.expected,
		},
		closer: file,
	}, nil
}

func (s *multipartReplaySource) Close() error {
	s.mu.Lock()
	if s.closed {
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	file := s.file
	s.file = nil
	if file != nil {
		s.closeErr = errors.Join(s.closeErr, file.Close())
	}
	err := s.closeErr
	s.mu.Unlock()
	return err
}

type capturingMultipartBody struct {
	body   *multipartRequestBody
	source *multipartReplaySource
}

func (b *capturingMultipartBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.source.Capture(p[:n])
	}
	if err != nil && !errors.Is(err, io.EOF) {
		b.source.Fail(err)
	}
	return n, err
}

func (b *capturingMultipartBody) Close() error {
	if !b.source.Ready() {
		b.source.Fail(errors.New("first multipart stream closed before replay capture completed"))
	}
	return b.body.Close()
}

func (b *capturingMultipartBody) Done() <-chan struct{} {
	return b.body.done
}
