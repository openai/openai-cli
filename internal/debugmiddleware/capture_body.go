package debugmiddleware

import (
	"fmt"
	"io"
	"mime"
	"strings"
	"sync"
)

const (
	DefaultCaptureBytes  = 16 * 1024
	MaximumCaptureBytes  = 1024 * 1024
	DefaultCaptureEvents = 64
	MaximumCaptureEvents = 4096
)

// CaptureOptions enables separate, bounded diagnostic views. These limits never
// restrict the request or response delivered to the transport or caller.
type CaptureOptions struct {
	Bodies, SSEFrames, EventTiming bool
	MaxBytes, MaxEvents            int
}

func (o CaptureOptions) normalized() CaptureOptions {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultCaptureBytes
	}
	if o.MaxBytes > MaximumCaptureBytes {
		o.MaxBytes = MaximumCaptureBytes
	}
	if o.MaxEvents <= 0 {
		o.MaxEvents = DefaultCaptureEvents
	}
	if o.MaxEvents > MaximumCaptureEvents {
		o.MaxEvents = MaximumCaptureEvents
	}
	return o
}

func mediaType(value string) string {
	t, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(t)
}

type bodyCapture struct {
	data     []byte
	limit    int
	observed uint64
	json     bool
	finished bool
}

func newBodyCapture(contentType string, limit int) *bodyCapture {
	t := mediaType(contentType)
	return &bodyCapture{limit: limit, json: t == "application/json" || strings.HasSuffix(t, "+json")}
}

func (c *bodyCapture) observe(p []byte) {
	if c.finished {
		return
	}
	c.observed += uint64(len(p))
	if c.json && len(c.data) < c.limit {
		c.data = append(c.data, p[:min(len(p), c.limit-len(c.data))]...)
	}
}

func (c *bodyCapture) finish(direction string, complete bool) string {
	if c.finished {
		return ""
	}
	c.finished = true
	state := "partial read"
	if complete {
		state = "complete read"
	}
	preview := "<omitted: non-JSON body>"
	if c.json {
		truncated := c.observed > uint64(len(c.data))
		if truncated {
			state += ", capture truncated"
		}
		preview = redactJSONCapture(c.data, truncated || !complete)
		if len(preview) > c.limit+128 {
			preview = "<omitted: rendered capture exceeds limit>"
		}
	}
	c.data = nil
	return fmt.Sprintf("%s body (redacted; %d bytes observed; %s): %s", direction, c.observed, state, preview)
}

// captureRequestBody observes transport reads without changing body ownership.
// All replay readers share one bounded snapshot, selected when a reader is used.
type captureRequestBody struct {
	io.ReadCloser
	state      *requestBodyCapture
	generation uint64
}

type requestBodyCapture struct {
	mu             sync.Mutex
	capture        *bodyCapture
	eof            bool
	expected       int64
	generation     uint64
	nextGeneration uint64
}

func newCaptureRequestBody(body io.ReadCloser, contentType string, limit int, expected int64) *captureRequestBody {
	return &captureRequestBody{ReadCloser: body, state: &requestBodyCapture{
		capture: newBodyCapture(contentType, limit), expected: expected,
	}}
}

func (b *captureRequestBody) replay(body io.ReadCloser) *captureRequestBody {
	b.state.mu.Lock()
	b.state.nextGeneration++
	generation := b.state.nextGeneration
	b.state.mu.Unlock()
	return &captureRequestBody{ReadCloser: body, state: b.state, generation: generation}
}

func (b *captureRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	s := b.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capture.finished || b.generation < s.generation {
		return n, err
	}
	if b.generation > s.generation {
		// Opening an unused replay must not discard an already observed body.
		if n == 0 && err != io.EOF {
			return n, err
		}
		s.generation = b.generation
		s.capture.data = s.capture.data[:0]
		s.capture.observed = 0
		s.eof = false
	}
	s.capture.observe(p[:n])
	if err == io.EOF {
		s.eof = true
	}
	return n, err
}

func (b *captureRequestBody) finish() string {
	s := b.state
	s.mu.Lock()
	defer s.mu.Unlock()
	complete := s.eof || (s.expected > 0 && s.capture.observed == uint64(s.expected))
	return s.capture.finish("request", complete)
}
