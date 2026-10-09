package custom

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type tokenizerPainterProbe struct {
	entered chan string
	resume  chan struct{}
	err     error
}

func (w *tokenizerPainterProbe) WriteContext(ctx context.Context, data []byte) (int, error) {
	w.entered <- string(data)
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-w.resume:
		if w.err != nil {
			return 0, w.err
		}
		return len(data), nil
	}
}

func TestTokenizerPainterKeepsLatestFrameWithoutBlockingInput(t *testing.T) {
	writer := &tokenizerPainterProbe{entered: make(chan string, 2), resume: make(chan struct{})}
	painter := newTokenizerPainter(writer, func() {})
	defer painter.Stop()
	painter.Control("initialize")
	require.Equal(t, "initialize", <-writer.entered)
	for i := 0; i < 1000; i++ {
		painter.Frame(strings.Repeat("x", 16384))
	}
	painter.Control("wrap")
	painter.Frame("latest")
	painter.mu.Lock()
	require.Equal(t, "wrap", painter.controls)
	require.Equal(t, "latest", painter.frame)
	painter.mu.Unlock()
	writer.resume <- struct{}{}
	require.Equal(t, "wraplatest", <-writer.entered)
	done := make(chan error, 1)
	go func() { done <- painter.Stop() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("painter shutdown remained blocked by terminal output")
	}
}

func TestTokenizerPainterRetainsOutputFailure(t *testing.T) {
	for _, failure := range []error{io.ErrClosedPipe, io.ErrShortWrite} {
		writer := &tokenizerPainterProbe{entered: make(chan string, 1), resume: make(chan struct{}, 1), err: failure}
		failed := make(chan struct{})
		painter := newTokenizerPainter(writer, func() { close(failed) })
		painter.Frame("frame")
		<-writer.entered
		writer.resume <- struct{}{}
		select {
		case <-failed:
		case <-time.After(time.Second):
			t.Fatal("output failure did not stop the editor")
		}
		require.True(t, errors.Is(painter.Stop(), failure))
	}
}
