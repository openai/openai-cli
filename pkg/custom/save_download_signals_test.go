package custom

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestDownloadSignalContextQueuedBeforeStop(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, tc := range []struct {
		name   string
		signal os.Signal
		code   int
	}{
		{"interrupt", os.Interrupt, 130},
		{"terminate", syscall.SIGTERM, 143},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for trial := range 64 {
				ctx, cancel := context.WithCancelCause(t.Context())
				signals := make(chan os.Signal, 1)
				signals <- tc.signal
				stop := watchDownloadSignals(ctx, cancel, signals, func() {})
				stop()
				var interrupted *downloadSignalError
				if !errors.As(context.Cause(ctx), &interrupted) || interrupted.exitCode != tc.code {
					t.Fatalf("trial %d: cause = %v; want signal status %d", trial, context.Cause(ctx), tc.code)
				}
				if !errors.Is(downloadContextError(ctx), context.Canceled) {
					t.Fatalf("trial %d: signal no longer unwraps to cancellation", trial)
				}
			}
		})
	}
}

func TestDownloadSignalContextIntentionalStop(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	signals := make(chan os.Signal, 1)
	stop := watchDownloadSignals(ctx, cancel, signals, func() {})
	stop()
	stop()
	if context.Cause(ctx) != context.Canceled {
		t.Fatalf("cause = %v; want intentional cancellation", context.Cause(ctx))
	}
	if t.Context().Err() != nil {
		t.Fatal("shutdown canceled the parent")
	}
}

func TestDownloadSignalContextFirstSignalWins(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second os.Signal
		code          int
	}{
		{"interrupt_then_terminate", os.Interrupt, syscall.SIGTERM, 130},
		{"terminate_then_interrupt", syscall.SIGTERM, os.Interrupt, 143},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			signals := make(chan os.Signal, 1)
			var queued sync.Once
			stop := watchDownloadSignals(ctx, cancel, signals, func() {
				queued.Do(func() {
					select {
					case signals <- tc.second:
					default:
						t.Error("fixture could not queue the second signal")
					}
				})
			})
			t.Cleanup(stop)
			signals <- tc.first
			waitDownloadSignalEvent(t, ctx.Done(), "first signal cancellation")
			firstCause := context.Cause(ctx)
			stop()
			if context.Cause(ctx) != firstCause {
				t.Fatal("queued second signal replaced the first cause")
			}
			var interrupted *downloadSignalError
			if !errors.As(firstCause, &interrupted) || interrupted.exitCode != tc.code {
				t.Fatalf("cause = %v; want first signal status %d", firstCause, tc.code)
			}
		})
	}
}

func TestDownloadSignalContextParentCauseWins(t *testing.T) {
	parentCause := errors.New("parent stopped")
	parent, cancelParent := context.WithCancelCause(t.Context())
	cancelParent(parentCause)
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGTERM
	stop := watchDownloadSignals(ctx, cancel, signals, func() {})
	stop()
	if context.Cause(ctx) != parentCause || ctx.Err() != context.Canceled {
		t.Fatalf("parent cause changed: err = %v; cause = %v", ctx.Err(), context.Cause(ctx))
	}
}

func TestDownloadSignalContextParentDeadlineWins(t *testing.T) {
	parent, cancelParent := context.WithDeadline(t.Context(), time.Unix(1, 0))
	defer cancelParent()
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signals <- os.Interrupt
	stop := watchDownloadSignals(ctx, cancel, signals, func() {})
	stop()
	if ctx.Err() != context.DeadlineExceeded || context.Cause(ctx) != context.DeadlineExceeded {
		t.Fatalf("deadline changed: err = %v; cause = %v", ctx.Err(), context.Cause(ctx))
	}
}

func TestDownloadSignalContextSignalBeforeParent(t *testing.T) {
	parent, cancelParent := context.WithCancelCause(t.Context())
	t.Cleanup(func() { cancelParent(nil) })
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	stop := watchDownloadSignals(ctx, cancel, signals, func() {})
	t.Cleanup(stop)
	signals <- syscall.SIGTERM
	waitDownloadSignalEvent(t, ctx.Done(), "signal cancellation")
	firstCause := context.Cause(ctx)
	cancelParent(errors.New("later parent cancellation"))
	stop()
	var interrupted *downloadSignalError
	if context.Cause(ctx) != firstCause || !errors.As(firstCause, &interrupted) || interrupted.exitCode != 143 {
		t.Fatalf("first signal cause changed: %v", context.Cause(ctx))
	}
}

func TestDownloadSignalContextConcurrentStop(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	signals := make(chan os.Signal, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	stop := watchDownloadSignals(ctx, cancel, signals, func() {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
	})
	t.Cleanup(stop)
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	finished := make(chan struct{}, 16)
	for range cap(finished) {
		go func() {
			stop()
			finished <- struct{}{}
		}()
	}
	waitDownloadSignalEvent(t, entered, "notification shutdown")
	select {
	case <-finished:
		t.Fatal("shutdown returned before notification shutdown completed")
	default:
	}
	unblock()
	for range cap(finished) {
		waitDownloadSignalEvent(t, finished, "concurrent shutdown")
	}
	stop()
	if calls.Load() != 1 || context.Cause(ctx) != context.Canceled {
		t.Fatalf("calls = %d; cause = %v", calls.Load(), context.Cause(ctx))
	}
}

func TestDownloadSignalContextStopJoinsWatcher(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	signals := make(chan os.Signal, 1)
	watcherEntered := make(chan struct{})
	stopEntered := make(chan struct{})
	releaseWatcher := make(chan struct{})
	var calls atomic.Int32
	stop := watchDownloadSignals(ctx, cancel, signals, func() {
		if calls.Add(1) == 1 {
			close(watcherEntered)
			<-releaseWatcher
		} else {
			close(stopEntered)
		}
	})
	t.Cleanup(stop)
	unblock := sync.OnceFunc(func() { close(releaseWatcher) })
	t.Cleanup(unblock)
	signals <- syscall.SIGTERM
	waitDownloadSignalEvent(t, watcherEntered, "watcher notification shutdown")
	finished := make(chan struct{})
	go func() {
		stop()
		close(finished)
	}()
	waitDownloadSignalEvent(t, stopEntered, "explicit notification shutdown")
	select {
	case <-finished:
		t.Fatal("shutdown returned while the watcher remained active")
	default:
	}
	unblock()
	waitDownloadSignalEvent(t, finished, "joined shutdown")
	var interrupted *downloadSignalError
	if !errors.As(context.Cause(ctx), &interrupted) || interrupted.exitCode != 143 {
		t.Fatalf("watcher signal cause lost: %v", context.Cause(ctx))
	}
}

func waitDownloadSignalEvent(t *testing.T, event <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
