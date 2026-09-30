package debugmiddleware

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestRequestQueryRedaction(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, query, requestURI, opaque string
	}{
		{name: "no query"},
		{name: "repeated and nested", query: "emails[]=fake%40example.test&emails[]=other%40example.test&metadata[private]=fake-secret"},
		{name: "malformed", query: "token=%zz;fake-secret&private-name"},
		{name: "request URI", requestURI: "/users?token=fake-secret"},
		{name: "absolute request URI", requestURI: "https://fake:fake-secret@example.test/users?token=fake-secret"},
		{name: "opaque", opaque: "//fake:fake-secret@example.test/users?token=fake-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, nextErr := range []error{nil, context.Canceled} {
				var logs bytes.Buffer
				logger := NewRequestLogger()
				logger.logger = log.New(&logs, "", 0)
				req, err := http.NewRequest(http.MethodGet, "https://example.test/users", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.URL.RawQuery = test.query
				req.URL.ForceQuery = true
				req.URL.Opaque = test.opaque
				req.URL.User = url.UserPassword("fake", "fake-secret")
				req.RequestURI = test.requestURI
				originalURL := *req.URL
				called := false
				_, err = logger.Middleware()(req, func(got *http.Request) (*http.Response, error) {
					called = true
					if got != req || !reflect.DeepEqual(*got.URL, originalURL) || got.RequestURI != test.requestURI {
						t.Errorf("Middleware(%+v) changed outbound request: %+v", test, got)
					}
					return nil, nextErr
				})
				if !called || err != nextErr {
					t.Errorf("Middleware(%+v) called=%v error=%v, want true and %v", test, called, err, nextErr)
				}
				want := "Request Content:\nGET /users HTTP/1.1\r\nHost: example.test\r\n\r\n\n"
				if got := logs.String(); got != want {
					t.Errorf("Middleware(%+v) log=%q, want %q", test, got, want)
				}
				if !reflect.DeepEqual(*req.URL, originalURL) || req.RequestURI != test.requestURI {
					t.Errorf("Middleware(%+v) mutated original request", test)
				}
			}
		})
	}
}

func TestRequestQueryRedactionPreservesEscapedPath(t *testing.T) {
	var logs bytes.Buffer
	logger := NewRequestLogger()
	logger.logger = log.New(&logs, "", 0)
	req, err := http.NewRequest(http.MethodGet, "https://example.test/files/a%2Fb?token=fake-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = logger.Middleware()(req, func(*http.Request) (*http.Response, error) { return nil, nil })
	if got := logs.String(); !strings.Contains(got, "GET /files/a%2Fb HTTP/1.1") || strings.Contains(got, "fake-secret") {
		t.Errorf("Middleware(escaped path) log=%q, want escaped path without query", got)
	}
}
