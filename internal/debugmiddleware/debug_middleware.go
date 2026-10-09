package debugmiddleware

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// For the time being these type definitions are duplicated here so that we can
// test this file in a non-generated context.
type (
	Middleware     = func(*http.Request, MiddlewareNext) (*http.Response, error)
	MiddlewareNext = func(*http.Request) (*http.Response, error)
)

const redactedPlaceholder = "<REDACTED>"

// Headers known to contain sensitive information like an API key. Note that this excludes
// `Authorization` and `Proxy-Authorization`, which are handled specially in `redactHeaders` below.
var sensitiveHeaders = []string{
	"api-key",
	"x-api-key",
	"cookie",
	"set-cookie",
}

// RequestLogger is a middleware that logs HTTP requests and responses.
type RequestLogger struct {
	logger           interface{ Printf(string, ...any) } // field for testability; usually log.Default()
	sensitiveHeaders []string                            // field for testability; usually sensitiveHeaders
	attempts         atomic.Uint64
}

// NewRequestLogger redacts known credential headers and any additional header names.
func NewRequestLogger(additionalSensitiveHeaders ...string) *RequestLogger {
	return &RequestLogger{
		logger:           log.Default(),
		sensitiveHeaders: slices.Concat(sensitiveHeaders, additionalSensitiveHeaders),
	}
}

func (m *RequestLogger) Middleware() Middleware {
	return func(req *http.Request, mn MiddlewareNext) (*http.Response, error) {
		redacted, err := m.redactRequest(req)
		if err != nil {
			return nil, err
		}
		if reqBytes, err := httputil.DumpRequest(redacted, false); err == nil {
			m.logger.Printf("Request Content:\n%s\n", reqBytes)
		}

		attempt := m.attempts.Add(1)
		started := time.Now()
		resp, err := mn(req)
		elapsed := time.Since(started)
		if err != nil || resp == nil {
			outcome := "transport failed"
			if req.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				outcome = "request canceled"
			} else if err == nil {
				outcome = "no response received"
			}
			m.logTiming(attempt, outcome, elapsed)
			return resp, err
		}
		// Status text comes from the standard library, never an arbitrary reason phrase.
		m.logger.Printf("HTTP attempt %d: response headers received after %d ms (%d %s)",
			attempt, elapsed.Round(time.Millisecond).Milliseconds(), resp.StatusCode, http.StatusText(resp.StatusCode))

		if respBytes, err := httputil.DumpResponse(m.redactResponse(resp), false); err == nil {
			m.logger.Printf("Response Content:\n%s\n", respBytes)
		}
		if resp.Body != nil {
			resp.Body = &timedResponseBody{body: resp.Body, context: req.Context(), logger: m, attempt: attempt, started: started}
		}

		return resp, err
	}
}

// redactRequest filters headers and omits query data on a logging-only copy.
func (m *RequestLogger) redactRequest(req *http.Request) (*http.Request, error) {
	redacted := req.Clone(req.Context())
	redacted.Header = m.redactHeaders(req.Header)
	// DumpRequest prefers RequestURI, and URL.RequestURI prefers Opaque.
	// Discard both alternate targets so only the escaped path is logged.
	redacted.RequestURI = ""
	if redacted.URL != nil {
		redacted.URL.Opaque = ""
		redacted.URL.User = nil
		// Omit the whole query, including sensitive keys and malformed values.
		redacted.URL.RawQuery = ""
		redacted.URL.ForceQuery = false
	}
	return redacted, nil
}

// redactResponse clones the response and redacts sensitive headers for logging.
func (m *RequestLogger) redactResponse(resp *http.Response) *http.Response {
	redacted := *resp
	redacted.Header = m.redactResponseHeaders(resp.Header)
	redacted.Trailer = m.redactResponseHeaders(resp.Trailer)

	return &redacted
}

func (m *RequestLogger) redactResponseHeaders(headers http.Header) http.Header {
	redactedHeaders := m.redactHeaders(headers)

	// Keep only operational response metadata. Unknown headers and trailers may
	// contain credentials even when their names were not supplied in the request.
	for header, values := range redactedHeaders {
		switch strings.ToLower(header) {
		case "content-type", "content-length", "date", "retry-after", "x-request-id":
			// redactHeaders has already applied explicitly sensitive names.
			continue
		}
		for i := range values {
			values[i] = redactedPlaceholder
		}
	}

	return redactedHeaders
}

func (m *RequestLogger) redactHeaders(headers http.Header) http.Header {
	redactedHeaders := headers.Clone()

	for header, values := range redactedHeaders {
		if slices.ContainsFunc(m.sensitiveHeaders, func(name string) bool { return strings.EqualFold(header, name) }) {
			for i := range values {
				values[i] = redactedPlaceholder
			}
			continue
		}

		if strings.EqualFold(header, "Authorization") || strings.EqualFold(header, "Proxy-Authorization") {
			for i, value := range values {
				// Keep the authentication scheme for more useful debug logging.
				if authKind, _, ok := strings.Cut(value, " "); ok {
					values[i] = authKind + " " + redactedPlaceholder
				} else {
					values[i] = redactedPlaceholder
				}
			}
		}
	}

	return redactedHeaders
}
