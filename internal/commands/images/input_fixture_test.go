package images

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const multipartTestTimeout = 5 * time.Second

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type recordingReadCloser struct {
	reader     io.Reader
	closeErr   error
	closeCount atomic.Int32
}

func (r *recordingReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *recordingReadCloser) Close() error {
	r.closeCount.Add(1)
	return r.closeErr
}

type closeUnblocksReader struct {
	err         error
	readStarted chan struct{}
	closed      chan struct{}
	startOnce   sync.Once
	closeOnce   sync.Once
	closeCount  atomic.Int32
}

func newCloseUnblocksReader(err error) *closeUnblocksReader {
	return &closeUnblocksReader{
		err:         err,
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (r *closeUnblocksReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.readStarted) })
	<-r.closed
	return 0, r.err
}

func (r *closeUnblocksReader) Close() error {
	r.closeCount.Add(1)
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}
