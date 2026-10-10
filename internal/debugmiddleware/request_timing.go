package debugmiddleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// timedResponseBody observes reads without buffering or taking ownership of the body.
// The mutex protects diagnostics only: Close must still unblock an active Read.
type timedResponseBody struct {
	body    io.ReadCloser
	context context.Context
	*responseTiming
}

// Only measurement state travels with the response request. It retains no body.
type responseTiming struct {
	logger   *RequestLogger
	attempt  uint64
	started  time.Time
	mu       sync.Mutex
	first    bool
	ended    bool
	deferred bool
	reported bool
	events   [3]bodyTimingEvent
}

type bodyTimingEvent struct {
	label   string
	elapsed time.Duration
}

type responseTimingKey struct{}

// DeferResponseBodyTiming records body diagnostics until the returned function
// runs. Binary consumers select it before reading and finish after their resource
// cleanup and final body close. SDK body wrappers do not hide the request metadata.
// Reporting can still wait for stderr; Read and Close in this mode cannot.
func DeferResponseBodyTiming(response *http.Response) func() {
	if response == nil || response.Request == nil {
		return func() {}
	}
	timing, _ := response.Request.Context().Value(responseTimingKey{}).(*responseTiming)
	if timing == nil {
		return func() {}
	}
	timing.mu.Lock()
	timing.deferred = true
	timing.mu.Unlock()
	return func() {
		timing.mu.Lock()
		if timing.reported {
			timing.mu.Unlock()
			return
		}
		timing.reported = true
		events := timing.events
		timing.events = [3]bodyTimingEvent{}
		timing.mu.Unlock()
		for _, event := range events {
			if event.label != "" {
				timing.logger.logTiming(timing.attempt, event.label, event.elapsed)
			}
		}
	}
}

// Caller holds mu. Each slot records one milestone, regardless of body size or
// read count. Repeated close failures retain the first failure's measurement.
func (t *responseTiming) report(slot int, label string, elapsed time.Duration) {
	if !t.deferred {
		t.logger.logTiming(t.attempt, label, elapsed)
	} else if !t.reported && t.events[slot].label == "" {
		t.events[slot] = bodyTimingEvent{label: label, elapsed: elapsed}
	}
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
		b.report(0, "first response data read", elapsed)
	}
	if err != nil {
		b.ended = true
		event := "response body read failed"
		if err == io.EOF {
			event = "response body fully consumed"
		} else if b.context.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			event = "response body read canceled"
		}
		b.report(1, event, elapsed)
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
		b.report(1, "response body closed before EOF", elapsed)
	}
	if err != nil {
		b.report(2, "response body close failed", elapsed)
	}
	return err
}
