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

// Measurement and bounded capture state travel with the response request.
// The state never owns the response body and clears captured bytes on completion.
type responseTiming struct {
	logger      *RequestLogger
	attempt     uint64
	started     time.Time
	mu          sync.Mutex
	first       bool
	ended       bool
	deferred    bool
	reported    bool
	events      [3]bodyTimingEvent
	capture     *bodyCapture
	sse         *sseCapture
	diagnostics []string
}

type bodyTimingEvent struct {
	label   string
	elapsed time.Duration
}

type responseTimingKey struct{}

// DeferResponseBodyTiming records all response body diagnostics until the returned function
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
		diagnostics := timing.diagnostics
		timing.events = [3]bodyTimingEvent{}
		timing.diagnostics = nil
		timing.mu.Unlock()
		for _, event := range events {
			if event.label != "" {
				timing.logger.logTiming(timing.attempt, event.label, event.elapsed)
			}
		}
		for _, diagnostic := range diagnostics {
			timing.logger.logger.Printf("HTTP attempt %d: %s", timing.attempt, diagnostic)
		}
	}
}

// Caller holds mu. Each slot records one milestone, regardless of body size or
// read count. Repeated close failures retain the first failure's measurement.
func (t *responseTiming) report(slot int, label string, elapsed time.Duration) {
	if !t.reported && t.events[slot].label == "" {
		t.events[slot] = bodyTimingEvent{label: label, elapsed: elapsed}
	}
}

// flush takes the bounded pending records under lock, then writes without the
// bookkeeping lock. Close must not wait for a diagnostic sink blocked in Read.
func (t *responseTiming) flush() {
	t.mu.Lock()
	if t.deferred {
		t.mu.Unlock()
		return
	}
	events, diagnostics := t.events, t.diagnostics
	t.events = [3]bodyTimingEvent{}
	t.diagnostics = nil
	t.mu.Unlock()
	for _, event := range events {
		if event.label != "" {
			t.logger.logTiming(t.attempt, event.label, event.elapsed)
		}
	}
	for _, diagnostic := range diagnostics {
		t.logger.logger.Printf("HTTP attempt %d: %s", t.attempt, diagnostic)
	}
}

func (t *responseTiming) addDiagnostic(message string) {
	if !t.reported && message != "" {
		t.diagnostics = append(t.diagnostics, message)
	}
}

func (t *responseTiming) finishCapture(complete bool, elapsed time.Duration) {
	if t.capture != nil {
		t.addDiagnostic(t.capture.finish("response", complete))
	}
	if t.sse != nil {
		t.sse.finish(elapsed, t.addDiagnostic)
		t.sse = nil
	}
}

func (m *RequestLogger) logTiming(attempt uint64, event string, elapsed time.Duration) {
	m.logger.Printf("HTTP attempt %d: %s after %d ms", attempt, event, elapsed.Round(time.Millisecond).Milliseconds())
}

func (b *timedResponseBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	elapsed := time.Since(b.started)
	b.mu.Lock()
	if b.ended {
		b.mu.Unlock()
		return n, err
	}
	if b.capture != nil {
		b.capture.observe(p[:n])
	}
	if b.sse != nil {
		b.sse.observe(p[:n], elapsed, b.addDiagnostic)
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
		b.finishCapture(err == io.EOF, elapsed)
	}
	b.mu.Unlock()
	b.flush()
	return n, err
}

func (b *timedResponseBody) Close() error {
	err := b.body.Close()
	elapsed := time.Since(b.started)
	b.mu.Lock()
	if !b.ended {
		b.ended = true
		b.report(1, "response body closed before EOF", elapsed)
		b.finishCapture(false, elapsed)
	}
	if err != nil {
		b.report(2, "response body close failed", elapsed)
	}
	b.mu.Unlock()
	b.flush()
	return err
}
