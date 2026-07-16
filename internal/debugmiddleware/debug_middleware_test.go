package debugmiddleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDebugMiddleware(t *testing.T) {
	t.Parallel()

	setup := func() (*RequestLogger, *bytes.Buffer) {
		var (
			logBuf     bytes.Buffer
			middleware = NewRequestLogger()
		)
		middleware.logger = log.New(&logBuf, "", 0)
		return middleware, &logBuf
	}

	t.Run("DoesNotRedactMostHeaders", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()

		const stainlessUserAgent = "Stainless"

		req := httptest.NewRequest("GET", "https://example.com", nil)
		req.Header.Set("User-Agent", stainlessUserAgent)

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			// The request sent down through middleware shouldn't be mutated.
			require.Equal(t, stainlessUserAgent, req.Header.Get("User-Agent"))

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)
		require.Contains(t, logBuf.String(), "User-Agent: "+stainlessUserAgent)
	})

	const secretToken = "secret-token"

	t.Run("RedactsAuthorizationHeader", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()

		req := httptest.NewRequest("GET", "https://example.com", nil)
		req.Header.Set("Authorization", secretToken)

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			// The request sent down through middleware shouldn't be mutated.
			require.Equal(t, secretToken, req.Header.Get("Authorization"))

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)
		require.Contains(t, logBuf.String(), "Authorization: "+redactedPlaceholder)
	})

	t.Run("RedactsOnlySecretInAuthorizationHeader", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()

		req := httptest.NewRequest("GET", "https://example.com", nil)
		req.Header.Set("Authorization", "Bearer "+secretToken)

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)
		require.Contains(t, logBuf.String(), "Authorization: Bearer "+redactedPlaceholder)
	})

	t.Run("RedactsMultipleAuthorizationHeaders", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()

		req := httptest.NewRequest("GET", "https://example.com", nil)
		req.Header.Add("Authorization", secretToken+"1")
		req.Header.Add("Authorization", secretToken+"2")

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			// The request sent down through middleware shouldn't be mutated.
			require.Equal(t, []string{secretToken + "1", secretToken + "2"}, req.Header.Values("Authorization"))

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)

		if strings.Count(logBuf.String(), "Authorization: "+redactedPlaceholder) != 2 {
			t.Error("expected exactly two redacted placeholders in authorization headers")
		}
	})

	const customAPIKeyHeader = "X-My-Api-Key"

	t.Run("RedactsSensitiveHeaders", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()

		middleware.sensitiveHeaders = []string{customAPIKeyHeader}

		req := httptest.NewRequest("GET", "https://example.com", nil)
		req.Header.Set(customAPIKeyHeader, secretToken)

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			// The request sent down through middleware shouldn't be mutated.
			require.Equal(t, secretToken, req.Header.Get(customAPIKeyHeader))

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)
		require.Contains(t, logBuf.String(), customAPIKeyHeader+": "+redactedPlaceholder)
	})

	t.Run("RedactsMultipleSensitiveHeaders", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()

		middleware.sensitiveHeaders = []string{customAPIKeyHeader}

		req := httptest.NewRequest("GET", "https://example.com", nil)
		req.Header.Add(customAPIKeyHeader, secretToken+"1")
		req.Header.Add(customAPIKeyHeader, secretToken+"2")

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			// The request sent down through middleware shouldn't be mutated.
			require.Equal(t, []string{secretToken + "1", secretToken + "2"}, req.Header.Values(customAPIKeyHeader))

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)
		require.Equal(t, 2, strings.Count(logBuf.String(), customAPIKeyHeader+": "+redactedPlaceholder))
	})

	t.Run("DoesNotConsumeRequestBodyWhenIoReader", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		middleware.sensitiveHeaders = []string{customAPIKeyHeader}

		const bodyContent = "test request body content"
		bodyReader := strings.NewReader(bodyContent)

		req := httptest.NewRequest("POST", "https://example.com", bodyReader)
		req.Header.Set("Authorization", secretToken)

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			// The request body should still be fully readable after the middleware runs
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, bodyContent, string(body))

			// The request sent down through middleware shouldn't be mutated.
			require.Equal(t, secretToken, req.Header.Get("Authorization"))

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)
		require.Contains(t, logBuf.String(), "Authorization: "+redactedPlaceholder)
		require.NotContains(t, logBuf.String(), bodyContent)
	})

	t.Run("RedactsSensitiveResponseHeaders", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		responseHeaders := http.Header{
			"sEt-CoOkIe":          {"session=" + secretToken + "1", "csrf=" + secretToken + "2"},
			"aUtHoRiZaTiOn":       {"Bearer " + secretToken + "3"},
			"pRoXy-AuThOrIzAtIoN": {"Basic " + secretToken + "4"},
			"X-Api-Key":           {secretToken + "5"},
			"X-Request-Id":        {"request-id"},
		}
		originalHeaders := responseHeaders.Clone()
		response := &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     responseHeaders,
			Body:       http.NoBody,
		}

		req := httptest.NewRequest("GET", "https://example.com", nil)
		resp, err := middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			return response, nil
		})
		require.NoError(t, err)

		logged := logBuf.String()
		for _, suffix := range []string{"1", "2", "3", "4", "5"} {
			require.NotContains(t, logged, secretToken+suffix)
		}
		require.Equal(t, 5, strings.Count(logged, redactedPlaceholder))
		require.Contains(t, logged, "Bearer "+redactedPlaceholder)
		require.Contains(t, logged, "Basic "+redactedPlaceholder)
		require.Contains(t, logged, "X-Request-Id: request-id")
		require.Same(t, response, resp)
		require.Equal(t, originalHeaders, resp.Header)
	})

	t.Run("DoesNotLogOrConsumeResponseBody", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()

		const bodyContent = "test response body content"

		req := httptest.NewRequest("GET", "https://example.com", nil)
		resp, err := middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(bodyContent)),
			}, nil
		})
		require.NoError(t, err)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, bodyContent, string(body))
		require.Contains(t, logBuf.String(), "Response Content:")
		require.NotContains(t, logBuf.String(), bodyContent)
	})
}
