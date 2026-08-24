package debugmiddleware

import (
	"log"
	"net/http"
	"net/http/httputil"
	"reflect"
	"strings"
)

// For the time being these type definitions are duplicated here so that we can
// test this file in a non-generated context.
type (
	Middleware     = func(*http.Request, MiddlewareNext) (*http.Response, error)
	MiddlewareNext = func(*http.Request) (*http.Response, error)
)

const redactedPlaceholder = "<REDACTED>"

// Headers known to contain sensitive information like an API key. Note that this excludes `Authorization`,
// which is handled specially in `redactHeaders` below.
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
}

// NewRequestLogger returns a new RequestLogger instance with default options.
func NewRequestLogger() *RequestLogger {
	return &RequestLogger{
		logger:           log.Default(),
		sensitiveHeaders: sensitiveHeaders,
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

		resp, err := mn(req)
		if err != nil || resp == nil {
			return resp, err
		}

		if respBytes, err := httputil.DumpResponse(m.redactResponse(resp), false); err == nil {
			m.logger.Printf("Response Content:\n%s\n", respBytes)
		}

		return resp, err
	}
}

// redactRequest redacts sensitive information from the request for logging
// purposes. If redaction is necessary, the request is cloned before mutating
// the original and that clone is returned. As a small optimization, the
// original request is returned unchanged if no redaction is necessary.
func (m *RequestLogger) redactRequest(req *http.Request) (*http.Request, error) {
	redactedHeaders := m.redactHeaders(req.Header)

	if reflect.DeepEqual(req.Header, redactedHeaders) {
		return req, nil
	}

	redacted := req.Clone(req.Context())
	redacted.Header = redactedHeaders
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

	// Redirect URLs can contain signed query parameters or embedded credentials.
	for header, values := range redactedHeaders {
		if strings.EqualFold(header, "Location") {
			for i := range values {
				values[i] = redactedPlaceholder
			}
		}
	}

	return redactedHeaders
}

func (m *RequestLogger) redactHeaders(headers http.Header) http.Header {
	redactedHeaders := headers.Clone()

	for header, values := range redactedHeaders {
		if strings.EqualFold(header, "Authorization") {
			for i, value := range values {
				// Keep the authentication scheme for more useful debug logging.
				if authKind, _, ok := strings.Cut(value, " "); ok {
					values[i] = authKind + " " + redactedPlaceholder
				} else {
					values[i] = redactedPlaceholder
				}
			}
			continue
		}

		for _, sensitiveHeader := range m.sensitiveHeaders {
			if strings.EqualFold(header, sensitiveHeader) {
				for i := range values {
					values[i] = redactedPlaceholder
				}
				break
			}
		}
	}

	return redactedHeaders
}
