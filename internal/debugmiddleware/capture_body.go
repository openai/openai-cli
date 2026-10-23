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

// captureRequestBody observes only reads requested by the transport. GetBody,
// ContentLength, replay policy, and the original body's ownership stay intact.
type captureRequestBody struct {
	io.ReadCloser
	mu       sync.Mutex
	capture  *bodyCapture
	eof      bool
	expected int64
}

func (b *captureRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	b.capture.observe(p[:n])
	if err == io.EOF {
		b.eof = true
	}
	b.mu.Unlock()
	return n, err
}

func (b *captureRequestBody) finish() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	complete := b.eof || (b.expected > 0 && b.capture.observed == uint64(b.expected))
	return b.capture.finish("request", complete)
}
