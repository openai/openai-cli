package debugmiddleware

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// timedResponseBody observes reads without buffering or taking ownership of the body.
// The mutex protects diagnostics only: Close must still unblock an active Read.
type timedResponseBody struct {
	body    io.ReadCloser
	context context.Context
	logger  *RequestLogger
	attempt uint64
	started time.Time
	mu      sync.Mutex
	first   bool
	ended   bool
}

func (m *RequestLogger) logTiming(attempt uint64, event string, elapsed time.Duration) {
	m.logger.Printf("HTTP attempt %d: %s after %d ms", attempt, event, elapsed.Round(time.Millisecond).Milliseconds())
}

func (b *timedResponseBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	elapsed := time.Since(b.started)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ended {
		return n, err
	}
	if n > 0 && !b.first {
		b.first = true
		b.logger.logTiming(b.attempt, "first response data read", elapsed)
	}
	if err != nil {
		b.ended = true
		event := "response body read failed"
		if err == io.EOF {
			event = "response body fully consumed"
		} else if b.context.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			event = "response body read canceled"
		}
		b.logger.logTiming(b.attempt, event, elapsed)
	}
	return n, err
}

func (b *timedResponseBody) Close() error {
	err := b.body.Close()
	elapsed := time.Since(b.started)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.ended {
		b.ended = true
		b.logger.logTiming(b.attempt, "response body closed before EOF", elapsed)
	}
	if err != nil {
		b.logger.logTiming(b.attempt, "response body close failed", elapsed)
	}
	return err
}
