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

		const userAgent = "OpenAI CLI"

		req := httptest.NewRequest("GET", "https://example.com", nil)
		req.Header.Set("User-Agent", userAgent)

		var nextMiddlewareRan bool
		middleware.Middleware()(req, func(req *http.Request) (*http.Response, error) {
			nextMiddlewareRan = true

			// The request sent down through middleware shouldn't be mutated.
			require.Equal(t, userAgent, req.Header.Get("User-Agent"))

			return &http.Response{}, nil
		})

		require.True(t, nextMiddlewareRan)
		require.Contains(t, logBuf.String(), "User-Agent: "+userAgent)
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

	t.Run("RedactsSensitiveResponseHeadersWithoutMutatingResponse", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		middleware.sensitiveHeaders = append([]string{customAPIKeyHeader}, middleware.sensitiveHeaders...)

		const signedLocation = "https://example.com/redirect?signature=synthetic-signature"
		response := &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Status:     "307 Temporary Redirect",
			Header: http.Header{
				"Authorization":    {"Bearer synthetic-response-token", "synthetic-secondary-token"},
				"Api-Key":          {"synthetic-api-key"},
				"X-Api-Key":        {"synthetic-x-api-key"},
				"X-My-Api-Key":     {"synthetic-custom-api-key"},
				"Cookie":           {"synthetic-cookie=secret"},
				"Set-Cookie":       {"synthetic-session=first; HttpOnly", "synthetic-session=second; Secure"},
				"Location":         {signedLocation},
				"X-Request-Id":     {"request-id"},
				"X-Response-Trace": {"trace-id"},
			},
		}
		originalHeaders := response.Header.Clone()

		req := httptest.NewRequest("GET", "https://example.com", nil)
		returned, err := middleware.Middleware()(req, func(*http.Request) (*http.Response, error) {
			return response, nil
		})
		require.NoError(t, err)
		require.Same(t, response, returned)
		require.Equal(t, originalHeaders, returned.Header)

		logged := logBuf.String()
		for _, secret := range []string{
			"synthetic-response-token",
			"synthetic-secondary-token",
			"synthetic-api-key",
			"synthetic-x-api-key",
			"synthetic-custom-api-key",
			"synthetic-cookie=secret",
			"synthetic-session=first",
			"synthetic-session=second",
			"synthetic-signature",
		} {
			require.NotContains(t, logged, secret)
		}

		require.Contains(t, logged, "Authorization: Bearer "+redactedPlaceholder)
		require.Contains(t, logged, "Authorization: "+redactedPlaceholder)
		require.Contains(t, logged, "Api-Key: "+redactedPlaceholder)
		require.Contains(t, logged, "X-Api-Key: "+redactedPlaceholder)
		require.Contains(t, logged, customAPIKeyHeader+": "+redactedPlaceholder)
		require.Contains(t, logged, "Cookie: "+redactedPlaceholder)
		require.Equal(t, 2, strings.Count(logged, "Set-Cookie: "+redactedPlaceholder))
		require.Contains(t, logged, "Location: "+redactedPlaceholder)
		require.Contains(t, logged, "X-Request-Id: request-id")
		require.Contains(t, logged, "X-Response-Trace: trace-id")
	})

	t.Run("RedactsRealHTTPResponseHeadersWithoutConsumingBody", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		const (
			bodyContent    = "synthetic response body"
			signedLocation = "https://example.com/redirect?signature=synthetic-integration-signature"
		)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			require.Equal(t, "Bearer synthetic-request-token", req.Header.Get("Authorization"))
			w.Header().Add("Set-Cookie", "synthetic-session=first; HttpOnly")
			w.Header().Add("Set-Cookie", "synthetic-session=second; Secure")
			w.Header().Set("Authorization", "Bearer synthetic-response-token")
			w.Header().Set("X-Api-Key", "synthetic-response-api-key")
			w.Header().Set("Location", signedLocation)
			w.Header().Set("X-Request-Id", "request-id")
			w.WriteHeader(http.StatusTemporaryRedirect)
			_, err := io.WriteString(w, bodyContent)
			require.NoError(t, err)
		}))
		t.Cleanup(server.Close)

		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer synthetic-request-token")

		response, err := middleware.Middleware()(req, client.Do)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
		require.Equal(t, []string{"synthetic-session=first; HttpOnly", "synthetic-session=second; Secure"}, response.Header.Values("Set-Cookie"))
		require.Equal(t, "Bearer synthetic-response-token", response.Header.Get("Authorization"))
		require.Equal(t, "synthetic-response-api-key", response.Header.Get("X-Api-Key"))
		require.Equal(t, signedLocation, response.Header.Get("Location"))

		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, bodyContent, string(body))

		logged := logBuf.String()
		for _, secret := range []string{
			"synthetic-request-token",
			"synthetic-response-token",
			"synthetic-response-api-key",
			"synthetic-session=first",
			"synthetic-session=second",
			"synthetic-integration-signature",
			bodyContent,
		} {
			require.NotContains(t, logged, secret)
		}
		require.Equal(t, 2, strings.Count(logged, "Authorization: Bearer "+redactedPlaceholder))
		require.Equal(t, 2, strings.Count(logged, "Set-Cookie: "+redactedPlaceholder))
		require.Contains(t, logged, "X-Api-Key: "+redactedPlaceholder)
		require.Contains(t, logged, "Location: "+redactedPlaceholder)
		require.Contains(t, logged, "X-Request-Id: request-id")
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
