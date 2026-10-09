package custom

import (
	"context"
	"errors"
	"io"
	"sync"
)

type tokenizerFrameOutput interface {
	WriteContext(context.Context, []byte) (int, error)
}

// Keep terminal backpressure outside the editor's event loop. Initialization
// controls retain order; only a superseded complete frame can be discarded.
type tokenizerPainter struct {
	output          tokenizerFrameOutput
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

func newTokenizerPainter(output tokenizerFrameOutput, onFailure context.CancelFunc) *tokenizerPainter {
	ctx, cancel := context.WithCancel(context.Background())
	p := &tokenizerPainter{output: output, ctx: ctx, cancel: cancel, onFailure: onFailure,
		wake: make(chan struct{}, 1), done: make(chan struct{})}
	go p.run()
	return p
}

func (p *tokenizerPainter) Control(data string) { p.enqueue(data, true) }
func (p *tokenizerPainter) Frame(data string)   { p.enqueue(data, false) }

func (p *tokenizerPainter) enqueue(data string, control bool) {
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

func (p *tokenizerPainter) run() {
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

func (p *tokenizerPainter) Stop() error {
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
