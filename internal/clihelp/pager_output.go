package clihelp

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/openai/openai-cli/internal/terminal"
)

// Unlike a full-screen repaint, each help append becomes permanent terminal
// history. Queue it as ordered output and acknowledge it before normal exit.
type helpPagerOutput struct {
	sink         terminal.FrameOutput
	painter      *terminal.Painter
	queued, sent atomic.Int64
	progress     chan struct{}
}

func newHelpPagerOutput(sink terminal.FrameOutput, cancel context.CancelFunc) *helpPagerOutput {
	w := &helpPagerOutput{sink: sink, progress: make(chan struct{}, 1)}
	w.painter = terminal.NewPainter(w, cancel)
	return w
}

func (w *helpPagerOutput) Write(data []byte) (int, error) {
	w.queued.Add(int64(len(data)))
	w.painter.Control(string(data))
	return len(data), nil
}

func (w *helpPagerOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	n, err := w.sink.WriteContext(ctx, data)
	w.sent.Add(int64(n))
	select {
	case w.progress <- struct{}{}:
	default:
	}
	return n, err
}

func (w *helpPagerOutput) finish(ctx context.Context, interrupted bool) error {
	if !interrupted {
		// A healthy terminal gets every queued append. A terminal that stops
		// reading after the user finishes must still release its native modes.
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		for w.sent.Load() < w.queued.Load() {
			select {
			case <-w.progress:
			case <-ctx.Done():
				return errors.Join(ctx.Err(), w.painter.Stop())
			}
		}
	}
	return w.painter.Stop()
}

// The event loop has stopped. Confirm each reset and query before reading
// its reply. Never wait indefinitely for the terminal to accept cleanup.
type helpCleanupOutput struct{ terminal.FrameOutput }

func (w helpCleanupOutput) Write(data []byte) (int, error) {
	if err := terminal.CloseFrame(w.FrameOutput, string(data), false); err != nil {
		return 0, err
	}
	return len(data), nil
}
