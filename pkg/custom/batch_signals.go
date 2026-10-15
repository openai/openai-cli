package custom

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

var batchOperationSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

type batchSignalError struct{ code int }

func (e *batchSignalError) Error() string        { return "local batch operation interrupted" }
func (e *batchSignalError) Is(target error) bool { return target == context.Canceled }

// Keep the received signal as a cause so owned cleanup retains its exit status.
func batchSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, batchOperationSignals...)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			}
			cancel(&batchSignalError{code: code})
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		signal.Stop(signals)
		cancel(nil)
		<-done
	}
}
