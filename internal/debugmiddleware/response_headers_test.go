package debugmiddleware

import (
	"net/http"
	"reflect"
	"testing"
)

func TestResponseHeaderPolicy(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"X-Auth-Token", "x-client-secret", "X-Amz-Security-Token", "CF-Access-Client-Secret", "X-Gateway", "Authorization", "Proxy-Authorization", "Location"} {
		t.Run(name, func(t *testing.T) {
			headers := http.Header{name: {"synthetic-first", "synthetic-second"}}
			original := headers.Clone()
			response := &http.Response{Header: headers, Trailer: headers.Clone()}
			got := NewRequestLogger().redactResponse(response)
			want := http.Header{name: {redactedPlaceholder, redactedPlaceholder}}
			if !reflect.DeepEqual(got.Header, want) || !reflect.DeepEqual(got.Trailer, want) {
				t.Errorf("redactResponse(%q) = headers %v, trailers %v; want %v", name, got.Header, got.Trailer, want)
			}
			if !reflect.DeepEqual(response.Header, original) || !reflect.DeepEqual(response.Trailer, original) {
				t.Errorf("redactResponse(%q) mutated original headers or trailers", name)
			}
		})
	}
	for _, name := range []string{"Content-Type", "content-length", "DATE", "Retry-After", "x-request-id"} {
		t.Run(name, func(t *testing.T) {
			headers := http.Header{name: {"operational-value"}}
			if got := NewRequestLogger().redactResponseHeaders(headers); !reflect.DeepEqual(got, headers) {
				t.Errorf("redactResponseHeaders(%q) = %v, want %v", name, got, headers)
			}
			want := http.Header{name: {redactedPlaceholder}}
			if got := NewRequestLogger(http.CanonicalHeaderKey(name)).redactResponseHeaders(headers); !reflect.DeepEqual(got, want) {
				t.Errorf("redactResponseHeaders(%q) with explicit sensitive name = %v, want %v", name, got, want)
			}
		})
	}
}
