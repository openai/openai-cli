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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

	t.Run("DoesNotPanicWhenNextReturnsNilResponseWithoutError", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)

		var (
			response *http.Response
			err      error
		)
		require.NotPanics(t, func() {
			response, err = middleware.Middleware()(req, func(*http.Request) (*http.Response, error) {
				return nil, nil
			})
		})
		require.Nil(t, response)
		require.NoError(t, err)
		require.Contains(t, logBuf.String(), "Request Content:")
		require.NotContains(t, logBuf.String(), "Response Content:")
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

	t.Run("RedactsCaseVariantRequestHeadersWithoutMutatingRequest", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		middleware.sensitiveHeaders = append([]string{customAPIKeyHeader}, middleware.sensitiveHeaders...)

		req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
		req.Header = http.Header{
			"Authorization": {"Bearer canonical-request-token"},
			"authorization": {"Bearer lowercase-request-token"},
			"X-Api-Key":     {"canonical-request-api-key"},
			"x-api-key":     {"lowercase-request-api-key"},
			"X-My-Api-Key":  {"canonical-request-custom-key"},
			"x-my-api-key":  {"lowercase-request-custom-key"},
			"Cookie":        {"canonical-request-cookie=secret"},
			"cookie":        {"lowercase-request-cookie=secret"},
			"X-Request-Id":  {"request-id"},
		}
		originalHeaders := req.Header.Clone()

		_, err := middleware.Middleware()(req, func(got *http.Request) (*http.Response, error) {
			require.Same(t, req, got)
			require.Equal(t, originalHeaders, got.Header)
			return &http.Response{}, nil
		})
		require.NoError(t, err)
		require.Equal(t, originalHeaders, req.Header)

		logged := logBuf.String()
		for _, secret := range []string{
			"canonical-request-token",
			"lowercase-request-token",
			"canonical-request-api-key",
			"lowercase-request-api-key",
			"canonical-request-custom-key",
			"lowercase-request-custom-key",
			"canonical-request-cookie=secret",
			"lowercase-request-cookie=secret",
		} {
			require.NotContains(t, logged, secret)
		}
		require.Contains(t, logged, "X-Request-Id: request-id")
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

	t.Run("RedactsCaseVariantResponseHeadersFromCustomTransport", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		middleware.sensitiveHeaders = append([]string{customAPIKeyHeader}, middleware.sensitiveHeaders...)

		const bodyContent = "synthetic response body"
		response := &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Body:          io.NopCloser(strings.NewReader(bodyContent)),
			ContentLength: int64(len(bodyContent)),
			Header: http.Header{
				"Authorization": {"Bearer canonical-authorization-secret"},
				"authorization": {"Bearer lowercase-authorization-secret"},
				"AUTHORIZATION": {"uppercase-authorization-secret"},
				"Api-Key":       {"canonical-api-key-secret"},
				"api-key":       {"lowercase-api-key-secret"},
				"X-Api-Key":     {"canonical-x-api-key-secret"},
				"x-api-key":     {"lowercase-x-api-key-secret"},
				"X-My-Api-Key":  {"canonical-custom-api-key-secret"},
				"x-my-api-key":  {"lowercase-custom-api-key-secret"},
				"Cookie":        {"canonical-cookie=secret"},
				"cookie":        {"lowercase-cookie=secret"},
				"Set-Cookie":    {"canonical-session=secret"},
				"set-cookie":    {"lowercase-session=secret", "second-lowercase-session=secret"},
				"Location":      {"https://example.com/redirect?signature=canonical-location-secret"},
				"location":      {"https://example.com/redirect?signature=lowercase-location-secret"},
				"X-Request-Id":  {"request-id"},
			},
		}
		originalHeaders := response.Header.Clone()

		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			response.Request = req
			return response, nil
		})}
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)

		returned, err := middleware.Middleware()(req, client.Do)
		require.NoError(t, err)
		require.Same(t, response, returned)
		require.Equal(t, originalHeaders, returned.Header)

		logged := logBuf.String()
		for _, secret := range []string{
			"canonical-authorization-secret",
			"lowercase-authorization-secret",
			"uppercase-authorization-secret",
			"canonical-api-key-secret",
			"lowercase-api-key-secret",
			"canonical-x-api-key-secret",
			"lowercase-x-api-key-secret",
			"canonical-custom-api-key-secret",
			"lowercase-custom-api-key-secret",
			"canonical-cookie=secret",
			"lowercase-cookie=secret",
			"canonical-session=secret",
			"lowercase-session=secret",
			"second-lowercase-session=secret",
			"canonical-location-secret",
			"lowercase-location-secret",
			bodyContent,
		} {
			require.NotContains(t, logged, secret)
		}
		lowercaseLog := strings.ToLower(logged)
		require.Equal(t, 2, strings.Count(lowercaseLog, "authorization: bearer <redacted>"))
		require.Contains(t, lowercaseLog, "authorization: <redacted>")
		require.Equal(t, 2, strings.Count(lowercaseLog, "x-api-key: <redacted>"))
		require.Equal(t, 3, strings.Count(lowercaseLog, "set-cookie: <redacted>"))
		require.Equal(t, 2, strings.Count(lowercaseLog, "location: <redacted>"))
		require.Contains(t, logged, "X-Request-Id: request-id")

		body, err := io.ReadAll(returned.Body)
		require.NoError(t, err)
		require.Equal(t, bodyContent, string(body))
	})

	t.Run("RedactsCaseVariantResponseTrailersFromCustomTransport", func(t *testing.T) {
		t.Parallel()

		middleware, logBuf := setup()
		middleware.sensitiveHeaders = append([]string{customAPIKeyHeader}, middleware.sensitiveHeaders...)
		response := &http.Response{
			StatusCode:       http.StatusOK,
			Status:           "200 OK",
			ProtoMajor:       1,
			ProtoMinor:       1,
			Body:             io.NopCloser(strings.NewReader("")),
			TransferEncoding: []string{"chunked"},
			Header:           http.Header{"X-Request-Id": {"request-id"}},
			Trailer: http.Header{
				"Authorization": {"Bearer canonical-trailer-authorization-secret"},
				"authorization": {"Bearer lowercase-trailer-authorization-secret"},
				"Api-Key":       {"canonical-trailer-api-key-secret"},
				"api-key":       {"lowercase-trailer-api-key-secret"},
				"X-Api-Key":     {"canonical-trailer-x-api-key-secret"},
				"x-api-key":     {"lowercase-trailer-x-api-key-secret"},
				"X-My-Api-Key":  {"canonical-trailer-custom-api-key-secret"},
				"x-my-api-key":  {"lowercase-trailer-custom-api-key-secret"},
				"Cookie":        {"canonical-trailer-cookie=secret"},
				"cookie":        {"lowercase-trailer-cookie=secret"},
				"Set-Cookie":    {"canonical-trailer-session=secret"},
				"set-cookie":    {"lowercase-trailer-session=secret", "second-lowercase-trailer-session=secret"},
				"Location":      {"https://example.com/?signature=canonical-trailer-location-secret"},
				"location":      {"https://example.com/?signature=lowercase-trailer-location-secret"},
				"X-Trace":       {"trace-id"},
			},
		}
		originalHeaders := response.Header.Clone()
		originalTrailers := response.Trailer.Clone()

		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			response.Request = req
			return response, nil
		})}
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)

		returned, err := middleware.Middleware()(req, client.Do)
		require.NoError(t, err)
		require.Same(t, response, returned)
		require.Equal(t, originalHeaders, returned.Header)
		require.Equal(t, originalTrailers, returned.Trailer)

		logged := logBuf.String()
		for _, secret := range []string{
			"canonical-trailer-authorization-secret",
			"lowercase-trailer-authorization-secret",
			"canonical-trailer-api-key-secret",
			"lowercase-trailer-api-key-secret",
			"canonical-trailer-x-api-key-secret",
			"lowercase-trailer-x-api-key-secret",
			"canonical-trailer-custom-api-key-secret",
			"lowercase-trailer-custom-api-key-secret",
			"canonical-trailer-cookie=secret",
			"lowercase-trailer-cookie=secret",
			"canonical-trailer-session=secret",
			"lowercase-trailer-session=secret",
			"second-lowercase-trailer-session=secret",
			"canonical-trailer-location-secret",
			"lowercase-trailer-location-secret",
		} {
			require.NotContains(t, logged, secret)
		}
		lowercaseLog := strings.ToLower(logged)
		require.Equal(t, 2, strings.Count(lowercaseLog, "authorization: bearer <redacted>"))
		require.Equal(t, 2, strings.Count(lowercaseLog, "x-api-key: <redacted>"))
		require.Equal(t, 3, strings.Count(lowercaseLog, "set-cookie: <redacted>"))
		require.Equal(t, 2, strings.Count(lowercaseLog, "location: <redacted>"))
		require.Contains(t, logged, "X-Trace: trace-id")
		require.Contains(t, logged, "X-Request-Id: request-id")
		body, err := io.ReadAll(returned.Body)
		require.NoError(t, err)
		require.Empty(t, body)
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
