package debugmiddleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
)

type timingLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *timingLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *timingLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func newTimingLogger() (*RequestLogger, *timingLogBuffer) {
	output := &timingLogBuffer{}
	middleware := NewRequestLogger()
	middleware.logger = log.New(output, "", 0)
	return middleware, output
}

type timingTestBody struct {
	read  func([]byte) (int, error)
	close func() error
}

func (b *timingTestBody) Read(p []byte) (int, error) { return b.read(p) }
func (b *timingTestBody) Close() error               { return b.close() }

func timingResponse(body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       body,
	}
}

func wrapTimingResponse(t *testing.T, middleware *RequestLogger, body io.ReadCloser) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://example.com/v1/models", nil)
	response := timingResponse(body)
	got, err := middleware.Middleware()(req, func(gotRequest *http.Request) (*http.Response, error) {
		require.Same(t, req, gotRequest)
		return response, nil
	})
	require.NoError(t, err)
	require.Same(t, response, got)
	return got
}

func timingMilliseconds(t *testing.T, output, event string) int64 {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^HTTP attempt 1: ` + regexp.QuoteMeta(event) + ` after ([0-9]+) ms(?: \([^\n]*\))?$`)
	matches := pattern.FindAllStringSubmatch(output, -1)
	require.Len(t, matches, 1, "expected exactly one %q event in %s", event, output)
	duration, err := strconv.ParseInt(matches[0][1], 10, 64)
	require.NoError(t, err)
	return duration
}

func TestRequestTimingSeparatesHeadersDataAndConsumption(t *testing.T) {
	middleware, output := newTimingLogger()
	var reads, closes int
	body := &timingTestBody{
		read: func(p []byte) (int, error) {
			reads++
			time.Sleep(20 * time.Millisecond)
			if reads == 1 {
				return copy(p, "synthetic-body-private"), nil
			}
			return 0, io.EOF
		},
		close: func() error { closes++; return nil },
	}
	req := httptest.NewRequest(http.MethodPost, "https://example.com/v1/models?token=synthetic-query-private", strings.NewReader("synthetic-prompt-private"))
	req.Header.Set("Authorization", "Bearer sk-fake-timing-private")
	response := timingResponse(body)
	response.Header.Set("Set-Cookie", "synthetic-cookie-private")
	got, err := middleware.Middleware()(req, func(gotRequest *http.Request) (*http.Response, error) {
		require.Same(t, req, gotRequest)
		require.Equal(t, "token=synthetic-query-private", gotRequest.URL.RawQuery)
		time.Sleep(20 * time.Millisecond)
		return response, nil
	})
	require.NoError(t, err)
	require.Same(t, response, got)
	require.Zero(t, reads, "middleware must not read ahead")
	require.Zero(t, closes, "caller owns the response body")
	headerTime := timingMilliseconds(t, output.String(), "response headers received")
	require.GreaterOrEqual(t, headerTime, int64(20))
	require.Contains(t, output.String(), " ms (200 OK)")
	require.NotContains(t, output.String(), "first response data")
	require.NotContains(t, output.String(), "fully consumed")

	time.Sleep(20 * time.Millisecond)
	buffer := make([]byte, 64)
	n, err := got.Body.Read(buffer)
	require.NoError(t, err)
	require.Equal(t, "synthetic-body-private", string(buffer[:n]))
	firstTime := timingMilliseconds(t, output.String(), "first response data read")
	require.GreaterOrEqual(t, firstTime-headerTime, int64(40))
	require.NotContains(t, output.String(), "fully consumed")
	n, err = got.Body.Read(buffer)
	require.Zero(t, n)
	require.ErrorIs(t, err, io.EOF)
	fullTime := timingMilliseconds(t, output.String(), "response body fully consumed")
	require.GreaterOrEqual(t, fullTime-firstTime, int64(20))
	require.NoError(t, got.Body.Close())
	require.Equal(t, 1, closes)
	require.NotContains(t, output.String(), "closed before EOF")
	for _, private := range []string{"synthetic-query-private", "synthetic-prompt-private", "sk-fake-timing-private", "synthetic-cookie-private", "synthetic-body-private"} {
		require.NotContains(t, output.String(), private)
	}
}

func TestRequestTimingReadBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		data   string
		zeroes int
	}{
		{name: "empty EOF"},
		{name: "data and EOF together", data: "synthetic-data"},
		{name: "zero reads before data", data: "synthetic-data", zeroes: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			middleware, output := newTimingLogger()
			var reads, closes int
			body := &timingTestBody{
				read: func(p []byte) (int, error) {
					reads++
					if reads <= test.zeroes {
						return 0, nil
					}
					if reads == test.zeroes+1 {
						return copy(p, test.data), io.EOF
					}
					return 0, io.EOF
				},
				close: func() error { closes++; return nil },
			}
			response := wrapTimingResponse(t, middleware, body)
			buffer := make([]byte, 64)
			for range test.zeroes {
				n, err := response.Body.Read(buffer)
				require.Zero(t, n)
				require.NoError(t, err)
				require.NotContains(t, output.String(), "first response data")
				require.NotContains(t, output.String(), "fully consumed")
			}
			n, err := response.Body.Read(buffer)
			require.Equal(t, test.data, string(buffer[:n]))
			require.ErrorIs(t, err, io.EOF)
			timingMilliseconds(t, output.String(), "response body fully consumed")
			if test.data == "" {
				require.NotContains(t, output.String(), "first response data")
			} else {
				timingMilliseconds(t, output.String(), "first response data read")
			}
			_, err = response.Body.Read(buffer)
			require.ErrorIs(t, err, io.EOF)
			require.NoError(t, response.Body.Close())
			require.NoError(t, response.Body.Close())
			require.Equal(t, 2, closes, "every Close must reach the underlying body")
			require.Equal(t, 1, strings.Count(output.String(), "response body fully consumed"))
			require.NotContains(t, output.String(), "closed before EOF")
		})
	}
}

func TestRequestTimingPreservesReadAndCloseErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		readErr  error
		closeErr error
		read     bool
		label    string
	}{
		{name: "early close", label: "response body closed before EOF"},
		{name: "close failure", closeErr: errors.New("synthetic-private-close-error"), label: "response body close failed"},
		{name: "read failure with partial data", read: true, readErr: errors.New("synthetic-private-read-error"), label: "response body read failed"},
		{name: "read cancellation", read: true, readErr: context.Canceled, label: "response body read canceled"},
		{name: "read deadline", read: true, readErr: context.DeadlineExceeded, label: "response body read canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			middleware, output := newTimingLogger()
			var closes int
			body := &timingTestBody{
				read:  func(p []byte) (int, error) { return copy(p, "synthetic-private-response"), test.readErr },
				close: func() error { closes++; return test.closeErr },
			}
			response := wrapTimingResponse(t, middleware, body)
			if test.read {
				buffer := make([]byte, 64)
				n, err := response.Body.Read(buffer)
				require.True(t, err == test.readErr, "Read must preserve the original error")
				require.Equal(t, "synthetic-private-response", string(buffer[:n]))
				timingMilliseconds(t, output.String(), "first response data read")
			}
			for range 2 {
				err := response.Body.Close()
				if test.closeErr == nil {
					require.NoError(t, err)
				} else {
					require.True(t, err == test.closeErr, "Close must preserve the original error")
				}
			}
			require.Equal(t, 2, closes)
			if test.closeErr == nil {
				timingMilliseconds(t, output.String(), test.label)
			} else {
				require.Equal(t, 2, strings.Count(output.String(), "response body close failed"))
				timingMilliseconds(t, output.String(), "response body closed before EOF")
			}
			require.NotContains(t, output.String(), "fully consumed")
			require.NotContains(t, output.String(), "synthetic-private")
		})
	}
}

func TestRequestTimingPreservesTransportResults(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		response *http.Response
		label    string
	}{
		{name: "failure", err: errors.New("https://example.com/?token=synthetic-private-error"), label: "transport failed"},
		{name: "canceled", err: context.Canceled, label: "request canceled"},
		{name: "deadline", err: context.DeadlineExceeded, label: "request canceled"},
		{name: "nil response", label: "no response received"},
		{name: "response and error", response: timingResponse(http.NoBody), err: errors.New("synthetic-private-error"), label: "transport failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			middleware, output := newTimingLogger()
			req := httptest.NewRequest(http.MethodGet, "https://example.com/v1/models", nil)
			got, err := middleware.Middleware()(req, func(*http.Request) (*http.Response, error) {
				return test.response, test.err
			})
			if test.err == nil {
				require.NoError(t, err)
			} else {
				require.True(t, err == test.err, "middleware must preserve the transport error")
			}
			if test.response == nil {
				require.Nil(t, got)
			} else {
				require.Same(t, test.response, got)
				require.Equal(t, http.NoBody, got.Body)
			}
			timingMilliseconds(t, output.String(), test.label)
			require.NotContains(t, output.String(), "synthetic-private")
			require.NotContains(t, output.String(), "response headers received")
		})
	}
}

func TestRequestTimingUsesStandardStatusDescription(t *testing.T) {
	middleware, output := newTimingLogger()
	req := httptest.NewRequest(http.MethodGet, "https://example.com/v1/models", nil)
	response := timingResponse(http.NoBody)
	response.StatusCode = http.StatusTeapot
	response.Status = "418 synthetic-private-status https://example.com/?token=synthetic-private-token"
	got, err := middleware.Middleware()(req, func(*http.Request) (*http.Response, error) {
		return response, nil
	})
	require.NoError(t, err)
	require.Same(t, response, got)
	require.Equal(t, "418 synthetic-private-status https://example.com/?token=synthetic-private-token", got.Status)
	var timingLines []string
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "HTTP attempt ") {
			timingLines = append(timingLines, line)
		}
	}
	require.Len(t, timingLines, 1)
	require.Regexp(t, `^HTTP attempt 1: response headers received after [0-9]+ ms \(418 I'm a teapot\)$`, timingLines[0])
	require.NotContains(t, timingLines[0], "synthetic-private")
	require.NotContains(t, timingLines[0], "https://")
	require.NoError(t, got.Body.Close())
}

func TestRequestTimingCloseUnblocksRead(t *testing.T) {
	middleware, output := newTimingLogger()
	reader, writer := io.Pipe()
	defer writer.Close()
	started := make(chan struct{})
	body := &timingTestBody{
		read: func(p []byte) (int, error) {
			close(started)
			return reader.Read(p)
		},
		close: reader.Close,
	}
	response := wrapTimingResponse(t, middleware, body)
	readDone := make(chan error, 1)
	go func() {
		_, err := response.Body.Read(make([]byte, 1))
		readDone <- err
	}()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- response.Body.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		_ = reader.Close()
		<-readDone
		<-closeDone
		t.Fatal("Close blocked behind Read")
	}
	select {
	case err := <-readDone:
		require.ErrorIs(t, err, io.ErrClosedPipe)
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not stop after Close")
	}
	require.NotContains(t, output.String(), "fully consumed")
	require.NotContains(t, output.String(), "first response data")
}

func TestRequestTimingCanceledBodyRead(t *testing.T) {
	middleware, output := newTimingLogger()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	body := &timingTestBody{
		read: func([]byte) (int, error) {
			close(started)
			<-ctx.Done()
			return 0, ctx.Err()
		},
		close: func() error { return nil },
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/v1/models", nil).WithContext(ctx)
	response, err := middleware.Middleware()(req, func(*http.Request) (*http.Response, error) {
		return timingResponse(body), nil
	})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := response.Body.Read(make([]byte, 1))
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Read did not return")
	}
	require.NoError(t, response.Body.Close())
	timingMilliseconds(t, output.String(), "response body read canceled")
	require.NotContains(t, output.String(), "fully consumed")
}

func TestRequestTimingConcurrentAttempts(t *testing.T) {
	middleware, output := newTimingLogger()
	const attempts = 24
	var workers sync.WaitGroup
	for range attempts {
		workers.Go(func() {
			req := httptest.NewRequest(http.MethodGet, "https://example.com/v1/models", nil)
			response, err := middleware.Middleware()(req, func(*http.Request) (*http.Response, error) {
				return timingResponse(io.NopCloser(strings.NewReader("synthetic-data"))), nil
			})
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			data, err := io.ReadAll(response.Body)
			if err != nil || string(data) != "synthetic-data" {
				t.Errorf("unexpected response: %q, %v", data, err)
			}
			if err := response.Body.Close(); err != nil {
				t.Errorf("Close failed: %v", err)
			}
		})
	}
	workers.Wait()
	for attempt := 1; attempt <= attempts; attempt++ {
		for _, event := range []string{"response headers received", "first response data read", "response body fully consumed"} {
			require.Equal(t, 1, strings.Count(output.String(), fmt.Sprintf("HTTP attempt %d: %s after ", attempt, event)))
		}
	}
	require.NotContains(t, output.String(), "closed before EOF")
}

func TestRequestTimingSDKRetry(t *testing.T) {
	middleware, output := newTimingLogger()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic-private-retry","type":"rate_limit_error"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"synthetic-model","object":"model","created":0,"owned_by":"test"}]}`)
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient(
		option.WithAPIKey("sk-fake-timing-retry"),
		option.WithBaseURL(server.URL+"/v1/"),
		option.WithHTTPClient(server.Client()),
		option.WithMaxRetries(1),
		option.WithMiddleware(middleware.Middleware()),
	)
	page, err := client.Models.List(t.Context())
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	require.Equal(t, "synthetic-model", page.Data[0].ID)
	require.Equal(t, int32(2), attempts.Load())
	for attempt, status := range []string{"429 Too Many Requests", "200 OK"} {
		pattern := fmt.Sprintf(`HTTP attempt %d: response headers received after [0-9]+ ms \(%s\)`, attempt+1, status)
		require.Regexp(t, pattern, output.String())
	}
	require.Equal(t, 1, strings.Count(output.String(), "HTTP attempt 1: response body closed before EOF"))
	require.NotContains(t, output.String(), "HTTP attempt 1: response body fully consumed")
	require.Equal(t, 1, strings.Count(output.String(), "HTTP attempt 2: response body fully consumed"))
	require.NotContains(t, output.String(), "synthetic-private-retry")
	require.NotContains(t, output.String(), "sk-fake-timing-retry")
	require.NotContains(t, output.String(), "synthetic-model")
}
