package debugmiddleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
)

type deferredTimingGateLogger struct {
	output  timingLogBuffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (l *deferredTimingGateLogger) Printf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if strings.HasPrefix(message, "HTTP attempt ") && !strings.Contains(message, "response headers received") {
		l.once.Do(func() { close(l.entered) })
		<-l.release
	}
	_, _ = fmt.Fprintln(&l.output, message)
}

func TestDeferredBodyTimingDoesNotBlockReadOrClose(t *testing.T) {
	logger := &deferredTimingGateLogger{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(logger.release) }) }
	t.Cleanup(release)
	middleware := NewRequestLogger()
	middleware.logger = logger
	closeErr := errors.New("synthetic-private-close-error")
	var closes atomic.Int32
	response := wrapTimingResponse(t, middleware, &timingTestBody{
		read: func(p []byte) (int, error) { return copy(p, "synthetic-data"), io.EOF },
		close: func() error {
			closes.Add(1)
			return closeErr
		},
	})
	finish := DeferResponseBodyTiming(response)
	type bodyResult struct {
		data     string
		readErr  error
		closeErr error
	}
	bodyDone := make(chan bodyResult, 1)
	go func() {
		buffer := make([]byte, 64)
		n, err := response.Body.Read(buffer)
		bodyDone <- bodyResult{string(buffer[:n]), err, response.Body.Close()}
	}()
	select {
	case result := <-bodyDone:
		require.Equal(t, "synthetic-data", result.data)
		require.ErrorIs(t, result.readErr, io.EOF)
		require.Same(t, closeErr, result.closeErr)
	case <-logger.entered:
		release()
		<-bodyDone
		t.Fatal("deferred Read or Close attempted to print a diagnostic")
	case <-time.After(2 * time.Second):
		t.Fatal("deferred Read or Close did not return")
	}
	require.Equal(t, int32(1), closes.Load())
	require.NotContains(t, logger.output.String(), "first response data")
	require.NotContains(t, logger.output.String(), "fully consumed")

	finishDone := make(chan struct{})
	go func() { finish(); close(finishDone) }()
	select {
	case <-logger.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("finish did not reach the diagnostic sink")
	}
	// A blocked sink must not retain the bookkeeping lock or body ownership.
	closeDone := make(chan error, 1)
	go func() { closeDone <- response.Body.Close() }()
	select {
	case err := <-closeDone:
		require.Same(t, closeErr, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the blocked finish diagnostic")
	}
	secondFinishDone := make(chan struct{})
	go func() { finish(); close(secondFinishDone) }()
	select {
	case <-secondFinishDone:
	case <-time.After(2 * time.Second):
		t.Fatal("repeated finish waited for the blocked diagnostic")
	}
	release()
	select {
	case <-finishDone:
	case <-time.After(2 * time.Second):
		t.Fatal("finish did not return after releasing diagnostics")
	}
	for _, event := range []string{"first response data read", "response body fully consumed", "response body close failed"} {
		timingMilliseconds(t, logger.output.String(), event)
	}
	require.Equal(t, int32(2), closes.Load())
	require.NotContains(t, logger.output.String(), "synthetic-private-close-error")
}

func TestDeferredBodyTimingRetainsMeasuredDurations(t *testing.T) {
	middleware, output := newTimingLogger()
	closeErr := errors.New("synthetic-private-close-error")
	var reads int
	body := &timingTestBody{
		read: func(p []byte) (int, error) {
			reads++
			time.Sleep(10 * time.Millisecond)
			if reads == 1 {
				return copy(p, "synthetic-data"), nil
			}
			return 0, io.EOF
		},
		close: func() error {
			time.Sleep(10 * time.Millisecond)
			return closeErr
		},
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/v1/files/file-test/content", nil)
	var dispatched time.Time
	beforeMiddleware := time.Now()
	response, err := middleware.Middleware()(req, func(*http.Request) (*http.Response, error) {
		dispatched = time.Now()
		return timingResponse(body), nil
	})
	require.NoError(t, err)
	finish := DeferResponseBodyTiming(response)
	type eventBoundary struct {
		event         string
		before, after time.Time
	}
	boundaries := []eventBoundary{{event: "first response data read"}, {event: "response body fully consumed"}, {event: "response body close failed"}}
	buffer := make([]byte, 64)
	boundaries[0].before = time.Now()
	n, err := response.Body.Read(buffer)
	boundaries[0].after = time.Now()
	require.NoError(t, err)
	require.Equal(t, "synthetic-data", string(buffer[:n]))
	boundaries[1].before = time.Now()
	n, err = response.Body.Read(buffer)
	boundaries[1].after = time.Now()
	require.Zero(t, n)
	require.ErrorIs(t, err, io.EOF)
	boundaries[2].before = time.Now()
	require.Same(t, closeErr, response.Body.Close())
	boundaries[2].after = time.Now()
	require.NotContains(t, output.String(), "first response data")

	// Reporting delay must not replace the original read and close timestamps.
	time.Sleep(40 * time.Millisecond)
	finish()
	for _, boundary := range boundaries {
		milliseconds := timingMilliseconds(t, output.String(), boundary.event)
		require.GreaterOrEqual(t, milliseconds, boundary.before.Sub(dispatched).Milliseconds()-1)
		require.LessOrEqual(t, milliseconds, boundary.after.Sub(beforeMiddleware).Milliseconds()+1)
	}
	firstOutput := output.String()
	finish()
	require.Equal(t, firstOutput, output.String())
}

func TestDeferredBodyTimingPreservesTerminalResults(t *testing.T) {
	for _, test := range []struct {
		name  string
		data  string
		err   error
		read  bool
		event string
	}{
		{name: "empty EOF", read: true, err: io.EOF, event: "response body fully consumed"},
		{name: "data and EOF", read: true, data: "synthetic-data", err: io.EOF, event: "response body fully consumed"},
		{name: "partial read failure", read: true, data: "synthetic-data", err: errors.New("synthetic-private-read-error"), event: "response body read failed"},
		{name: "canceled", read: true, err: context.Canceled, event: "response body read canceled"},
		{name: "deadline", read: true, err: context.DeadlineExceeded, event: "response body read canceled"},
		{name: "early close", event: "response body closed before EOF"},
	} {
		t.Run(test.name, func(t *testing.T) {
			middleware, output := newTimingLogger()
			var reads, closes int
			response := wrapTimingResponse(t, middleware, &timingTestBody{
				read: func(p []byte) (int, error) {
					reads++
					return copy(p, test.data), test.err
				},
				close: func() error { closes++; return nil },
			})
			finish := DeferResponseBodyTiming(response)
			if test.read {
				buffer := make([]byte, 64)
				n, err := response.Body.Read(buffer)
				require.Equal(t, test.data, string(buffer[:n]))
				require.True(t, err == test.err, "Read must preserve the original error")
				require.Equal(t, 1, reads)
			}
			require.NoError(t, response.Body.Close())
			require.Equal(t, 1, closes)
			require.NotContains(t, output.String(), test.event)
			finish()
			timingMilliseconds(t, output.String(), test.event)
			if test.data != "" {
				timingMilliseconds(t, output.String(), "first response data read")
			} else {
				require.NotContains(t, output.String(), "first response data")
			}
			require.NotContains(t, output.String(), "synthetic-private")
			firstOutput := output.String()
			finish()
			require.Equal(t, firstOutput, output.String())
			require.Equal(t, 1, closes, "finish must not close the body")
		})
	}
}

func TestDeferredBodyTimingKeepsOnlyFirstCloseFailure(t *testing.T) {
	middleware, output := newTimingLogger()
	closeErrors := []error{errors.New("synthetic-private-first-error"), errors.New("synthetic-private-second-error")}
	var closes int
	response := wrapTimingResponse(t, middleware, &timingTestBody{
		read: func([]byte) (int, error) { t.Fatal("unexpected Read"); return 0, nil },
		close: func() error {
			err := closeErrors[closes]
			closes++
			return err
		},
	})
	finish := DeferResponseBodyTiming(response)
	require.Same(t, closeErrors[0], response.Body.Close())
	time.Sleep(10 * time.Millisecond)
	require.Same(t, closeErrors[1], response.Body.Close())
	require.Equal(t, 2, closes)
	require.NotContains(t, output.String(), "response body close failed")
	finish()
	closed := timingMilliseconds(t, output.String(), "response body closed before EOF")
	failed := timingMilliseconds(t, output.String(), "response body close failed")
	require.Equal(t, closed, failed, "retain the first Close boundary, not a later failure")
	require.NotContains(t, output.String(), "synthetic-private")
}

func TestDeferredBodyTimingPreservesFinalRequestContext(t *testing.T) {
	type contextKey string
	for _, redirected := range []bool{false, true} {
		t.Run(fmt.Sprintf("redirected=%v", redirected), func(t *testing.T) {
			middleware, output := newTimingLogger()
			dispatchedContext, cancelDispatched := context.WithCancel(context.WithValue(t.Context(), contextKey("request"), "dispatched"))
			defer cancelDispatched()
			dispatched := httptest.NewRequest(http.MethodGet, "https://example.com/original", nil).WithContext(dispatchedContext)
			dispatched.Header.Set("X-Request-Id", "original-request")
			deadline := time.Now().Add(time.Minute)
			finalContext, cancelFinal := context.WithDeadline(context.WithValue(t.Context(), contextKey("request"), "final"), deadline)
			defer cancelFinal()
			final := httptest.NewRequest(http.MethodGet, "https://example.com/redirected", nil).WithContext(finalContext)
			final.Header.Set("X-Request-Id", "final-request")
			original := timingResponse(io.NopCloser(strings.NewReader("synthetic-data")))
			selected := dispatched
			if redirected {
				original.Request = final
				selected = final
			}
			response, err := middleware.Middleware()(dispatched, func(got *http.Request) (*http.Response, error) {
				require.Same(t, dispatched, got)
				return original, nil
			})
			require.NoError(t, err)
			require.NotNil(t, response.Request)
			require.NotSame(t, selected, response.Request)
			require.Equal(t, selected.URL, response.Request.URL)
			require.Equal(t, selected.Method, response.Request.Method)
			require.Equal(t, selected.Header, response.Request.Header)
			require.Equal(t, selected.Context().Value(contextKey("request")), response.Request.Context().Value(contextKey("request")))
			selectedDeadline, selectedHasDeadline := selected.Context().Deadline()
			gotDeadline, gotHasDeadline := response.Request.Context().Deadline()
			require.Equal(t, selectedHasDeadline, gotHasDeadline)
			require.Equal(t, selectedDeadline, gotDeadline)
			require.Same(t, dispatchedContext, dispatched.Context())
			require.Same(t, finalContext, final.Context())
			require.Equal(t, "https://example.com/original", dispatched.URL.String())
			require.Equal(t, "https://example.com/redirected", final.URL.String())
			finish := DeferResponseBodyTiming(response)
			data, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, "synthetic-data", string(data))
			require.NoError(t, response.Body.Close())
			require.NotContains(t, output.String(), "fully consumed")
			if redirected {
				cancelDispatched()
				require.NoError(t, response.Request.Context().Err(), "metadata must use the final request context")
				cancelFinal()
			} else {
				cancelDispatched()
			}
			require.ErrorIs(t, response.Request.Context().Err(), context.Canceled)
			finish()
			timingMilliseconds(t, output.String(), "response body fully consumed")
		})
	}
}

func TestDeferredBodyTimingSurvivesSDKTimeoutWrapper(t *testing.T) {
	middleware, output := newTimingLogger()
	var closes atomic.Int32
	client := openai.NewClient(
		option.WithAPIKey("sk-fake-deferred-timing"),
		option.WithBaseURL("https://example.com/v1/"),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(time.Minute),
		option.WithMiddleware(middleware.Middleware()),
		option.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			response := timingResponse(&timingTestBody{
				read:  func(p []byte) (int, error) { return copy(p, "synthetic-data"), io.EOF },
				close: func() error { closes.Add(1); return nil },
			})
			response.Request = req
			return response, nil
		})}),
	)
	response, err := client.Files.Content(t.Context(), "file-synthetic")
	require.NoError(t, err)
	_, directTimingBody := response.Body.(*timedResponseBody)
	require.False(t, directTimingBody, "test must exercise an SDK body wrapper")
	finish := DeferResponseBodyTiming(response)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "synthetic-data", string(data))
	require.NoError(t, response.Body.Close())
	require.Equal(t, int32(1), closes.Load())
	require.NotContains(t, output.String(), "first response data")
	finish()
	timingMilliseconds(t, output.String(), "first response data read")
	timingMilliseconds(t, output.String(), "response body fully consumed")
	require.Equal(t, int32(1), closes.Load())
}

func TestDeferredBodyTimingPreservesSDKTimeoutExpiry(t *testing.T) {
	middleware, output := newTimingLogger()
	serverCanceled := make(chan error, 1)
	handlerDone := make(chan struct{})
	connectionClosed := make(chan struct{})
	writeResult := make(chan error, 1)
	var closedOnce sync.Once
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer close(handlerDone)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, err := io.WriteString(w, "x")
		writeResult <- err
		w.(http.Flusher).Flush()
		// The second byte remains unavailable until the SDK cancels this request.
		<-req.Context().Done()
		serverCanceled <- req.Context().Err()
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closedOnce.Do(func() { close(connectionClosed) })
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	httpClient := server.Client()
	t.Cleanup(httpClient.CloseIdleConnections)
	client := openai.NewClient(
		option.WithAPIKey("sk-fake-deferred-timeout"),
		option.WithBaseURL(server.URL+"/v1/"),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(time.Second),
		option.WithMiddleware(middleware.Middleware()),
		option.WithHTTPClient(httpClient),
	)
	// This outer deadline bounds failures without providing the timeout under test.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response, err := client.Files.Content(ctx, "file-synthetic")
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	finish := DeferResponseBodyTiming(response)
	data, readErr := io.ReadAll(response.Body)
	require.Equal(t, "x", string(data))
	require.ErrorIs(t, readErr, context.DeadlineExceeded)
	require.NotErrorIs(t, readErr, io.EOF)
	require.NoError(t, ctx.Err(), "the SDK timeout must expire before the outer safety deadline")
	require.ErrorIs(t, response.Request.Context().Err(), context.DeadlineExceeded)
	require.NoError(t, response.Body.Close())
	require.NotContains(t, output.String(), "response body read canceled")
	require.NotContains(t, output.String(), "response body fully consumed")
	finish()
	timingMilliseconds(t, output.String(), "first response data read")
	timingMilliseconds(t, output.String(), "response body read canceled")
	require.NotContains(t, output.String(), "response body fully consumed")
	require.NoError(t, <-writeResult)
	select {
	case err := <-serverCanceled:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("SDK timeout did not cancel the server request")
	}
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server handler did not finish after cancellation")
	}
	select {
	case <-connectionClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("SDK timeout left the response connection open")
	}
}

func TestDeferredBodyTimingWithoutMetadataDoesNothing(t *testing.T) {
	for _, response := range []*http.Response{nil, {}, {Request: httptest.NewRequest(http.MethodGet, "https://example.com/", nil)}} {
		var reads, closes int
		if response != nil {
			response.Body = &timingTestBody{
				read:  func([]byte) (int, error) { reads++; return 0, io.EOF },
				close: func() error { closes++; return nil },
			}
		}
		finish := DeferResponseBodyTiming(response)
		require.NotNil(t, finish)
		finish()
		finish()
		require.Zero(t, reads)
		require.Zero(t, closes)
	}
}
