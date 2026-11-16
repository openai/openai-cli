package terminal

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// FrameOutput writes terminal frames with cancellation support.
type FrameOutput interface {
	WriteContext(context.Context, []byte) (int, error)
}

// Painter keeps terminal backpressure outside the editor's event loop. Initialization
// controls retain order; only a superseded complete frame can be discarded.
type Painter struct {
	output          FrameOutput
	ctx             context.Context
	cancel          context.CancelFunc
	onFailure       context.CancelFunc
	mu              sync.Mutex
	controls, frame string
	err             error
	closed          bool
	wake            chan struct{}
	done            chan struct{}
}

// NewPainter starts a painter that invokes onFailure when output fails.
func NewPainter(output FrameOutput, onFailure context.CancelFunc) *Painter {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Painter{output: output, ctx: ctx, cancel: cancel, onFailure: onFailure,
		wake: make(chan struct{}, 1), done: make(chan struct{})}
	go p.run()
	return p
}

func (p *Painter) Control(data string) { p.enqueue(data, true) }
func (p *Painter) Frame(data string)   { p.enqueue(data, false) }

func (p *Painter) enqueue(data string, control bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.err != nil {
		return
	}
	if control {
		p.controls += data
	} else {
		p.frame = data
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Painter) run() {
	defer close(p.done)
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.wake:
		}
		p.mu.Lock()
		data := p.controls + p.frame
		p.controls, p.frame = "", ""
		p.mu.Unlock()
		if data == "" {
			continue
		}
		n, err := p.output.WriteContext(p.ctx, []byte(data))
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil {
			p.mu.Lock()
			if !p.closed || !errors.Is(err, context.Canceled) {
				p.err = err
			}
			p.mu.Unlock()
			p.onFailure()
			return
		}
	}
}

func (p *Painter) Stop() error {
	p.mu.Lock()
	p.closed = true
	p.controls, p.frame = "", ""
	p.cancel()
	p.mu.Unlock()
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// CloseFrame bounds the final cleanup write when a terminal stops accepting
// output. Callers remain responsible for restoring native input/output modes.
func CloseFrame(output FrameOutput, frame string, interrupted bool) error {
	if frame == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	n, err := output.WriteContext(ctx, []byte(frame))
	if interrupted && errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

// FramePainter queues ordered controls and replaceable complete frames.
type FramePainter interface {
	Control(string)
	Frame(string)
}
