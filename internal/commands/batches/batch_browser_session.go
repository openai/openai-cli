package batches

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// A session keeps one terminal lifecycle across menus, then restores scrollback before output.
// Only the controller calls its methods. The host owns each screen until its choice is returned.
type batchBrowserSession struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	phase     *batchBrowserPhase
	failure   error
	run       func(context.Context, tea.Model, batchPickerInputPolicy) error
	runInline func(context.Context, *batchBrowserScreen) error
}

type batchBrowserPhase struct {
	requests chan batchBrowserScreenRequest
	done     chan struct{}
	err      error // Published by closing done.
}

type batchBrowserScreenRequest struct {
	screen *batchBrowserScreen
	chosen chan struct{}
	quit   bool
}

func newBatchBrowserSession(ctx context.Context, cancel context.CancelCauseFunc) *batchBrowserSession {
	return &batchBrowserSession{ctx: ctx, cancel: cancel, run: runBatchPickerModel, runInline: runBatchBrowserScreen}
}

func (s *batchBrowserSession) show(_ context.Context, screen *batchBrowserScreen) error {
	if s.ctx.Err() != nil {
		return context.Cause(s.ctx)
	}
	if screen.kind == "return" {
		if err := s.close(); err != nil {
			return err
		}
		if err := s.runInline(s.ctx, screen); err != nil {
			s.failure = err
			s.cancel(err)
			return err
		}
		return nil
	}
	screen.chosen = ""
	screen.closed = false
	request := batchBrowserScreenRequest{screen: screen, chosen: make(chan struct{}, 1)}
	if s.phase == nil {
		phase := &batchBrowserPhase{requests: make(chan batchBrowserScreenRequest), done: make(chan struct{})}
		s.phase = phase
		phaseContext, stop := context.WithCancel(s.ctx)
		host := &batchBrowserHost{ctx: phaseContext, cancel: s.cancel, requests: phase.requests, active: request}
		go func() {
			err := s.run(phaseContext, host, batchBrowserInputPolicy())
			host.stopWatchingInput()
			stop() // Release any pending menu-request command after Tea exits.
			phase.err = errors.Join(context.Cause(s.ctx), err)
			if phase.err != nil {
				s.cancel(phase.err)
			}
			close(phase.done)
		}()
	} else {
		select {
		case s.phase.requests <- request:
		case <-s.phase.done:
			return s.phase.err
		case <-s.ctx.Done():
			return context.Cause(s.ctx)
		}
	}
	select {
	case <-request.chosen:
		return nil
	case <-s.phase.done:
		return s.phase.err
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	}
}

func (s *batchBrowserSession) beforeOutput(context.Context) error { return s.close() }

func (s *batchBrowserSession) close() error {
	if s.phase != nil {
		select {
		case s.phase.requests <- batchBrowserScreenRequest{quit: true}:
		case <-s.phase.done:
		}
		<-s.phase.done // Includes raw-input restoration and owned output-worker cleanup.
		s.failure = errors.Join(s.phase.err, s.failure)
		s.phase = nil
	}
	return s.failure
}

type batchBrowserHost struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	requests <-chan batchBrowserScreenRequest
	active   batchBrowserScreenRequest
	cached   tea.View
	width    int
	height   int
	watch    func() bool
	watched  chan struct{}
}

func (h *batchBrowserHost) setPickerContext(ctx context.Context) {
	h.watched = make(chan struct{})
	h.watch = context.AfterFunc(ctx, func() {
		defer close(h.watched)
		cause := context.Cause(ctx)
		// Normal helper teardown cancels its private context. Keyboard errors carry
		// their own WorkflowError, including during a graceful output drain.
		if cause != context.Canceled || h.ctx.Err() != nil {
			h.cancel(cause)
		}
	})
}

func (h *batchBrowserHost) stopWatchingInput() {
	if h.watch != nil && !h.watch() {
		<-h.watched
	}
}

func (h *batchBrowserHost) next() tea.Cmd {
	return func() tea.Msg {
		select {
		case request := <-h.requests:
			return request
		case <-h.ctx.Done():
			return tea.QuitMsg{}
		}
	}
}

func (h *batchBrowserHost) Init() tea.Cmd {
	return tea.Batch(h.active.screen.Init(), h.next())
}

func (h *batchBrowserHost) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := message.(tea.WindowSizeMsg); ok {
		h.width, h.height = size.Width, size.Height
	}
	if request, ok := message.(batchBrowserScreenRequest); ok {
		if request.quit {
			return h, tea.Quit
		}
		h.active = request
		h.active.screen.Update(tea.WindowSizeMsg{Width: h.width, Height: h.height})
		return h, tea.Batch(h.active.screen.Init(), h.next())
	}
	if h.active.screen == nil {
		return h, nil // Keep the immutable menu visible while the controller uses its screen.
	}
	previous := h.active.screen.View()
	_, command := h.active.screen.Update(message)
	if h.active.screen.closed {
		h.cached = previous
		chosen := h.active.chosen
		h.active = batchBrowserScreenRequest{}
		chosen <- struct{}{}
		return h, nil // A child menu's Quit ends its choice, not the terminal session.
	}
	return h, command
}

func (h *batchBrowserHost) View() tea.View {
	if h.active.screen != nil {
		return h.active.screen.View()
	}
	return h.cached
}
