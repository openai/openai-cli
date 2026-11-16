package batches

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestBatchBrowserHostKeepsDetachedViewAndLatestSize(t *testing.T) {
	first := newBatchBrowserActions("batch_one")
	chosen := make(chan struct{}, 1)
	host := &batchBrowserHost{ctx: t.Context(), active: batchBrowserScreenRequest{screen: first, chosen: chosen}}
	host.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	host.Init()
	before := host.View()
	_, command := host.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, command, "child Quit must not end the menu phase")
	<-chosen
	require.Equal(t, "output", first.chosen)
	require.Nil(t, host.active.screen)
	first.title = "controller owns this screen now"
	first.width = 999
	host.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	require.Equal(t, before, host.View(), "pending work must retain the immutable view")
	next := newBatchBrowserPath(newBatchBrowserActions("batch_one"), "output")
	host.Update(batchBrowserScreenRequest{screen: next, chosen: make(chan struct{}, 1)})
	require.Equal(t, 30, next.width)
	require.Equal(t, 7, next.height)
	require.Contains(t, host.View().Content, "File:")
	require.True(t, host.View().AltScreen)
	_, command = host.Update(batchBrowserScreenRequest{quit: true})
	require.IsType(t, tea.QuitMsg{}, command())
	require.NotEmpty(t, host.View().Content, "phase teardown retains its last frame")
	require.True(t, host.View().AltScreen)
}

type batchBrowserObservedHost struct {
	tea.Model
	attached chan<- struct{}
}

func (h *batchBrowserObservedHost) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, command := h.Model.Update(msg)
	h.Model = model
	if request, ok := msg.(batchBrowserScreenRequest); ok && !request.quit {
		h.attached <- struct{}{}
	}
	return h, command
}

func batchBrowserTestReceive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for browser session")
		var zero T
		return zero
	}
}

func TestBatchBrowserSessionRetainsMenusAndJoinsOutputPhase(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	session := newBatchBrowserSession(ctx, cancel)
	defer session.close()
	programs, attached := make(chan *tea.Program, 2), make(chan struct{}, 4)
	draining, drained := make(chan struct{}), make(chan struct{})
	var runs atomic.Int32
	session.run = func(ctx context.Context, model tea.Model, _ batchPickerInputPolicy) error {
		count := runs.Add(1)
		outputContext, stop := context.WithCancelCause(ctx)
		defer stop(nil)
		host := model.(*batchBrowserHost)
		host.setPickerContext(outputContext)
		model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30}) // The real helper sizes before Init.
		program := tea.NewProgram(&batchBrowserObservedHost{Model: model, attached: attached}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithWindowSize(100, 30), tea.WithContext(ctx), tea.WithoutRenderer(), tea.WithoutSignalHandler())
		programs <- program
		_, err := program.Run()
		if count == 1 {
			close(draining)
			<-drained
		}
		return err
	}
	inline := 0
	session.runInline = func(context.Context, *batchBrowserScreen) error {
		require.Nil(t, session.phase)
		inline++
		return nil
	}
	show := func(screen *batchBrowserScreen) <-chan error {
		done := make(chan error, 1)
		go func() { done <- session.show(ctx, screen) }()
		return done
	}
	actions := newBatchBrowserActions("batch_one")
	done := show(actions)
	program := batchBrowserTestReceive(t, programs)
	program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NoError(t, batchBrowserTestReceive(t, done))
	done = show(newBatchBrowserPath(newBatchBrowserActions("batch_one"), "output"))
	batchBrowserTestReceive(t, attached)
	program.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NoError(t, batchBrowserTestReceive(t, done))
	require.Equal(t, int32(1), runs.Load(), "menu navigation must reuse the terminal session")
	closed := make(chan error, 1)
	go func() { closed <- session.beforeOutput(ctx) }()
	batchBrowserTestReceive(t, draining)
	select {
	case <-closed:
		t.Fatal("output began before terminal cleanup finished")
	default:
	}
	close(drained)
	require.NoError(t, batchBrowserTestReceive(t, closed))
	require.NoError(t, ctx.Err(), "normal helper teardown must not cancel browsing")
	require.NoError(t, session.show(ctx, newBatchBrowserReturn("Output shown above.")))
	require.Equal(t, 1, inline)
	require.True(t, actions.closed)
	done = show(actions) // Reusing a closed screen must survive the helper's pre-Init size event.
	program = batchBrowserTestReceive(t, programs)
	program.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NoError(t, batchBrowserTestReceive(t, done))
	require.Equal(t, "back", actions.chosen)
	require.Equal(t, int32(2), runs.Load())
	require.NoError(t, session.close())
}

func TestBatchBrowserSessionCancelsWorkWithOriginalTerminalCause(t *testing.T) {
	for name, cause := range map[string]error{
		"interrupt": &WorkflowError{message: "Synthetic interruption", code: 130, cause: context.Canceled},
		"eof":       &WorkflowError{message: "Synthetic input ended", code: 1},
		"overflow":  &WorkflowError{message: "Synthetic pending-input overflow", code: 1},
		"output":    io.ErrClosedPipe,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			session := newBatchBrowserSession(ctx, cancel)
			defer session.close()
			programs := make(chan *tea.Program, 1)
			interrupts := make(chan context.CancelCauseFunc, 1)
			outputFailures := make(chan error, 1)
			session.run = func(ctx context.Context, model tea.Model, _ batchPickerInputPolicy) error {
				outputContext, stop := context.WithCancelCause(ctx)
				defer stop(nil)
				model.(*batchBrowserHost).setPickerContext(outputContext)
				model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
				program := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithWindowSize(100, 30), tea.WithContext(outputContext), tea.WithoutRenderer(), tea.WithoutSignalHandler())
				programs <- program
				interrupts <- stop
				_, err := program.Run()
				select {
				case failure := <-outputFailures:
					return failure
				default:
					return batchContextError(errors.Join(ctx.Err(), context.Cause(ctx), err))
				}
			}
			done := make(chan error, 1)
			go func() { done <- session.show(ctx, newBatchBrowserActions("batch_one")) }()
			program := batchBrowserTestReceive(t, programs)
			stop := batchBrowserTestReceive(t, interrupts)
			program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.NoError(t, batchBrowserTestReceive(t, done))
			require.NoError(t, ctx.Err())
			if name == "output" {
				outputFailures <- cause
				program.Kill()
			} else {
				stop(cause)
			}
			batchBrowserTestReceive(t, ctx.Done())
			require.ErrorIs(t, context.Cause(ctx), cause, "in-flight work must receive the terminal cause")
			err := session.close()
			require.ErrorIs(t, err, cause)
			var exit *WorkflowError
			if name != "output" {
				require.ErrorAs(t, err, &exit)
				want := 1
				if name == "interrupt" {
					want = 130
				}
				require.Equal(t, want, exit.ExitCode())
			} else {
				require.False(t, errors.As(err, &exit), "output failure must not become interruption code 130")
			}
		})
	}
}
