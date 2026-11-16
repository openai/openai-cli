package debugmiddleware

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fullCaptureLogger(options CaptureOptions) (*RequestLogger, *timingLogBuffer) {
	output := &timingLogBuffer{}
	middleware := NewRequestLoggerWithOptions(options)
	middleware.logger = log.New(output, "", 0)
	return middleware, output
}

func fullCaptureResponse(t *testing.T, middleware *RequestLogger, contentType string, body io.ReadCloser) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://example.test/v1/responses", nil)
	response := timingResponse(body)
	response.Header.Set("Content-Type", contentType)
	got, err := middleware.Middleware()(request, func(*http.Request) (*http.Response, error) {
		return response, nil
	})
	require.NoError(t, err)
	return got
}

func TestFullCaptureRequestIsLazyAndPreservesReplay(t *testing.T) {
	payload := `{"input":"synthetic-private-upload","stream":true}`
	request, err := http.NewRequest(http.MethodPost, "https://example.test/v1/responses", strings.NewReader(payload))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	original := request.Body
	var reads, closes int
	tracked := &timingTestBody{
		read:  func(p []byte) (int, error) { reads++; return original.Read(p) },
		close: func() error { closes++; return original.Close() },
	}
	request.Body = tracked
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
	_, err = middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
		require.Zero(t, reads, "diagnostics must not pre-read the request")
		require.Zero(t, closes)
		require.Equal(t, int64(len(payload)), got.ContentLength)
		require.Equal(t, request.Header, got.Header)
		require.NotNil(t, got.GetBody)
		replay, replayErr := got.GetBody()
		require.NoError(t, replayErr)
		replayed, replayErr := io.ReadAll(replay)
		require.NoError(t, replayErr)
		require.NoError(t, replay.Close())
		require.Equal(t, payload, string(replayed))
		require.Zero(t, reads, "GetBody must retain the original replay function")
		gotBytes, readErr := io.ReadAll(got.Body)
		require.NoError(t, readErr)
		require.Equal(t, payload, string(gotBytes))
		require.NoError(t, got.Body.Close())
		require.Equal(t, 1, closes)
		require.NotContains(t, output.String(), "request body (")
		return timingResponse(nil), nil
	})
	require.NoError(t, err)
	require.Same(t, tracked, request.Body, "the caller's request must keep its original body")
	require.Equal(t, 1, closes, "the middleware must not close the transport's body")
	require.Contains(t, output.String(), "complete read")
	require.Contains(t, output.String(), `"stream":true`)
	require.NotContains(t, output.String(), "synthetic-private-upload")
}

func TestFullCaptureEmptyRequestPreservesHTTPFraming(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			request, err := http.NewRequest(method, "https://example.test/empty", strings.NewReader(""))
			require.NoError(t, err)
			require.True(t, request.Body == http.NoBody)
			var before, after bytes.Buffer
			require.NoError(t, request.Write(&before))
			middleware, _ := fullCaptureLogger(CaptureOptions{Bodies: true})
			_, err = middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
				require.NoError(t, got.Write(&after))
				return timingResponse(nil), nil
			})
			require.NoError(t, err)
			require.Equal(t, before.String(), after.String(), "diagnostics must preserve empty-request framing")
		})
	}
}

func TestFullCaptureRequestSnapshotDoesNotTakeLateTransportOwnership(t *testing.T) {
	payload := `{"input":"synthetic-private-late-upload"}`
	source := strings.NewReader(payload)
	var reads, closes int
	request := httptest.NewRequest(http.MethodPost, "https://example.test/v1/responses", nil)
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	request.Body = &timingTestBody{
		read:  func(p []byte) (int, error) { reads++; return source.Read(p) },
		close: func() error { closes++; return nil },
	}
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
	var owned io.ReadCloser
	_, err := middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
		owned = got.Body
		first := make([]byte, 4)
		n, readErr := owned.Read(first)
		require.NoError(t, readErr)
		require.Equal(t, payload[:4], string(first[:n]))
		return timingResponse(nil), nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	require.Zero(t, closes)
	snapshot := output.String()
	require.Contains(t, snapshot, "request body (redacted; 4 bytes observed; partial read)")
	rest, err := io.ReadAll(owned)
	require.NoError(t, err)
	require.Equal(t, payload[4:], string(rest))
	require.NoError(t, owned.Close())
	require.Equal(t, 1, closes)
	require.Equal(t, snapshot, output.String(), "late transport reads must not emit another snapshot")
	require.NotContains(t, snapshot, "synthetic-private-late-upload")
}

func TestFullCaptureRequestPreservesRedirectReplay(t *testing.T) {
	payload := `{"input":"synthetic-private-redirect"}`
	request, err := http.NewRequest(http.MethodPost, "https://example.test/first", strings.NewReader(payload))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	var paths, bodies []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			return nil, readErr
		}
		if closeErr := request.Body.Close(); closeErr != nil {
			return nil, closeErr
		}
		paths = append(paths, request.URL.Path)
		bodies = append(bodies, string(data))
		response := timingResponse(io.NopCloser(strings.NewReader("{}")))
		response.Request = request
		if len(paths) == 1 {
			response.StatusCode = http.StatusTemporaryRedirect
			response.Status = "307 Temporary Redirect"
			response.Header.Set("Location", "/second")
		}
		return response, nil
	})}
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
	response, err := middleware.Middleware()(request, client.Do)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, []string{"/first", "/second"}, paths)
	require.Equal(t, []string{payload, payload}, bodies)
	require.Equal(t, "/second", response.Request.URL.Path)
	require.NotContains(t, output.String(), "synthetic-private-redirect")
}

func TestFullCaptureResponsePreservesReadAndCloseErrors(t *testing.T) {
	readFailure := errors.New("synthetic-private-read-error")
	closeFailure := errors.New("synthetic-private-close-error")
	for _, terminal := range []error{io.EOF, readFailure} {
		t.Run(terminal.Error(), func(t *testing.T) {
			middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
			var reads, closes int
			payload := `{"input":"synthetic-private-response"}`
			response := fullCaptureResponse(t, middleware, "application/json", &timingTestBody{
				read: func(p []byte) (int, error) {
					reads++
					switch reads {
					case 1:
						return 0, nil
					case 2:
						return copy(p, payload), terminal
					default:
						return copy(p, "recovered bytes"), io.EOF
					}
				},
				close: func() error { closes++; return closeFailure },
			})
			buffer := make([]byte, 256)
			n, err := response.Body.Read(buffer)
			require.Zero(t, n)
			require.NoError(t, err)
			require.NotContains(t, output.String(), "first response data read")
			n, err = response.Body.Read(buffer)
			require.Equal(t, payload, string(buffer[:n]))
			require.True(t, err == terminal, "Read must preserve exact error identity")
			n, err = response.Body.Read(buffer)
			require.Equal(t, "recovered bytes", string(buffer[:n]))
			require.True(t, err == io.EOF)
			require.Same(t, closeFailure, response.Body.Close())
			require.Equal(t, 1, closes)
			require.Equal(t, 1, strings.Count(output.String(), "response body ("))
			if terminal != io.EOF {
				require.NotContains(t, output.String(), "response body fully consumed")
				require.Contains(t, output.String(), "partial read")
			}
			for _, private := range []string{"synthetic-private-response", readFailure.Error(), closeFailure.Error(), "recovered bytes"} {
				require.NotContains(t, output.String(), private)
			}
		})
	}
}

func TestFullCaptureJSONPrivacyAcrossEveryReadBoundary(t *testing.T) {
	payload := `{"input":"synthetic-private-prompt\u001b","model":"synthetic-private-model","synthetic-private-key":{"usage":{"total_tokens":456789}},"metadata":{"stream":true},"stream":true}`
	for split := 0; split <= len(payload); split++ {
		middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
		body := io.MultiReader(strings.NewReader(payload[:split]), strings.NewReader(payload[split:]))
		response := fullCaptureResponse(t, middleware, "application/problem+json; charset=utf-8", io.NopCloser(body))
		got, err := io.ReadAll(response.Body)
		require.NoError(t, err, "split %d", split)
		require.Equal(t, payload, string(got), "split %d", split)
		require.NoError(t, response.Body.Close())
		require.Contains(t, output.String(), `"stream":true`, "split %d", split)
		for _, private := range []string{"synthetic-private", "456789", "\x1b"} {
			require.NotContains(t, output.String(), private, "split %d", split)
		}
	}
}

func TestFullCaptureOmissionsDoNotRejectOrChangeBodies(t *testing.T) {
	for _, fixture := range []struct {
		name, contentType, payload, label string
		limit                             int
	}{
		{"invalid JSON", "application/json", `{"input":"synthetic-private-broken`, "omitted:", 1024},
		{"truncated JSON", "application/json", `{"input":"` + strings.Repeat("synthetic-private-large", 20000) + `"}`, "capture truncated", 64},
		{"binary", "application/octet-stream", "\x00\x1b\xffsynthetic-private-binary", "non-JSON body", 64},
		{"multipart", "multipart/form-data; boundary=fake", "--fake\r\nsynthetic-private-upload\r\n--fake--", "non-JSON body", 64},
		{"malformed content type", "application/json; broken", `{"input":"synthetic-private-content-type"}`, "non-JSON body", 64},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true, MaxBytes: fixture.limit})
			response := fullCaptureResponse(t, middleware, fixture.contentType, io.NopCloser(strings.NewReader(fixture.payload)))
			got, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, fixture.payload, string(got))
			require.NoError(t, response.Body.Close())
			require.Contains(t, output.String(), fixture.label)
			require.NotContains(t, output.String(), "synthetic-private")
			timing := response.Body.(*timedResponseBody).responseTiming
			require.Nil(t, timing.capture.data, "completed capture must release retained payload bytes")
		})
	}
}

func TestFullCaptureSSEPrivacyAndTimingAcrossEveryReadBoundary(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		payload := "\xef\xbb\xbf: synthetic-private-comment" + ending + ending +
			"id: synthetic-private-id" + ending +
			"event: synthetic-private-event" + ending +
			"synthetic-private-field: synthetic-private-value" + ending +
			`data: {"input":"synthetic-private-data","stream":true}` + ending + ending +
			"data: [DONE]" + ending + ending +
			"data: synthetic-private-incomplete"
		for split := 0; split <= len(payload); split++ {
			middleware, output := fullCaptureLogger(CaptureOptions{SSEFrames: true, EventTiming: true})
			body := io.MultiReader(strings.NewReader(payload[:split]), strings.NewReader(payload[split:]))
			response := fullCaptureResponse(t, middleware, "text/event-stream; charset=utf-8", io.NopCloser(body))
			got, err := io.ReadAll(response.Body)
			require.NoError(t, err, "ending %q split %d", ending, split)
			require.Equal(t, payload, string(got), "ending %q split %d", ending, split)
			require.NoError(t, response.Body.Close())
			logs := output.String()
			require.Equal(t, 2, strings.Count(logs, "complete frame observed"), "ending %q split %d", ending, split)
			require.Equal(t, 3, strings.Count(logs, "SSE frame "), "ending %q split %d", ending, split)
			require.Contains(t, logs, "SSE incomplete trailing frame")
			require.Contains(t, logs, "data=[DONE]")
			require.Contains(t, logs, `"stream":true`)
			require.NotContains(t, logs, "synthetic-private")
			require.NotContains(t, logs, "\xef\xbb\xbf", "wire diagnostic must escape the BOM")
		}
	}
}

func TestFullCaptureSSEMalformedDataAndUnknownLinesRemainPrivate(t *testing.T) {
	payload := ":synthetic-private-comment\nsynthetic-private-bare-name\n" +
		"data: {\"input\":\"synthetic-private-partial\n\n" +
		"data: synthetic-private-not-json\n\n" +
		"data\n\n"
	middleware, output := fullCaptureLogger(CaptureOptions{SSEFrames: true, EventTiming: true})
	response := fullCaptureResponse(t, middleware, "text/event-stream", io.NopCloser(strings.NewReader(payload)))
	got, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, payload, string(got))
	require.NoError(t, response.Body.Close())
	require.Equal(t, 3, strings.Count(output.String(), "complete frame observed"))
	require.Contains(t, output.String(), "omitted: invalid or incomplete JSON")
	require.NotContains(t, output.String(), "synthetic-private")
}

func TestFullCaptureSSECarriageReturnTimesBoundaryBeforeNextRead(t *testing.T) {
	for _, following := range []string{"", "\n", "data: {}\r\r"} {
		t.Run(fmt.Sprintf("following_%q", following), func(t *testing.T) {
			capture := newSSECapture(CaptureOptions{SSEFrames: true, EventTiming: true, MaxBytes: 1024, MaxEvents: 8})
			var records []string
			emit := func(record string) { records = append(records, record) }
			capture.observe([]byte("data: {}\r\r"), 10*time.Millisecond, emit)
			require.Equal(t, []string{"SSE event 1 complete frame observed after 10 ms (gap 10 ms)"}, records)
			capture.observe([]byte(following), 30*time.Millisecond, emit)
			capture.finish(50*time.Millisecond, emit)
			logs := strings.Join(records, "\n")
			wantEvents := 1
			if following == "data: {}\r\r" {
				wantEvents = 2
				require.Contains(t, logs, "SSE event 2 complete frame observed after 30 ms (gap 20 ms)")
			}
			require.Equal(t, wantEvents, strings.Count(logs, "complete frame observed"))
			require.Equal(t, wantEvents, strings.Count(logs, "SSE frame "))
			if following == "\n" {
				require.Contains(t, logs, `"data: <REDACTED>\r\r\n" data={}`)
			} else {
				require.Contains(t, logs, `"data: <REDACTED>\r\r" data={}`)
			}
			require.NotContains(t, logs, "incomplete trailing frame")
		})
	}
}

func TestFullCaptureOptionsDoNotEnableOtherDiagnosticCategories(t *testing.T) {
	for _, fixture := range []struct {
		name                         string
		options                      CaptureOptions
		wantBody, wantSSE, wantEvent bool
	}{
		{name: "default"},
		{name: "body", options: CaptureOptions{Bodies: true}, wantBody: true},
		{name: "SSE", options: CaptureOptions{SSEFrames: true}, wantSSE: true},
		{name: "event timing", options: CaptureOptions{EventTiming: true}, wantEvent: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			middleware, output := fullCaptureLogger(fixture.options)
			payload := "data: {\"input\":\"synthetic-private-isolation\"}\n\n"
			response := fullCaptureResponse(t, middleware, "text/event-stream", io.NopCloser(strings.NewReader(payload)))
			got, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, payload, string(got))
			require.NoError(t, response.Body.Close())
			logs := output.String()
			require.Equal(t, fixture.wantBody, strings.Contains(logs, "response body ("))
			require.Equal(t, fixture.wantSSE, strings.Contains(logs, "SSE frame "))
			require.Equal(t, fixture.wantEvent, strings.Contains(logs, "complete frame observed"))
			require.NotContains(t, logs, "synthetic-private")
		})
	}
}

func TestFullCaptureBudgetsBoundDeferredRecordsWithoutCappingPayload(t *testing.T) {
	const maximumBytes, maximumEvents, eventCount = 256, 3, 1200
	payload := strings.Repeat("data: {\"input\":\"synthetic-private-event\"}\n\n", eventCount)
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true, SSEFrames: true, EventTiming: true, MaxBytes: maximumBytes, MaxEvents: maximumEvents})
	response := fullCaptureResponse(t, middleware, "text/event-stream", io.NopCloser(strings.NewReader(payload)))
	finish := DeferResponseBodyTiming(response)
	timing := response.Body.(*timedResponseBody).responseTiming
	var got bytes.Buffer
	buffer := make([]byte, 97)
	for {
		n, err := response.Body.Read(buffer)
		got.Write(buffer[:n])
		timing.mu.Lock()
		retained := len(timing.diagnostics)
		if timing.sse != nil {
			require.LessOrEqual(t, len(timing.sse.raw), maximumBytes)
		}
		timing.mu.Unlock()
		require.LessOrEqual(t, retained, 2*maximumEvents+3, "retained records must not grow with event count")
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.Equal(t, payload, got.String())
	require.NoError(t, response.Body.Close())
	require.NotContains(t, output.String(), "SSE event ")
	require.NotContains(t, output.String(), "SSE frame ")
	finish()
	require.Equal(t, maximumEvents, strings.Count(output.String(), "complete frame observed"))
	require.Contains(t, output.String(), fmt.Sprintf("%d complete data frames observed", eventCount))
	require.NotContains(t, output.String(), "synthetic-private-event")
	timing.mu.Lock()
	require.Empty(t, timing.diagnostics)
	require.Nil(t, timing.sse)
	timing.mu.Unlock()
}

func TestFullCaptureDefersEveryResponseDiagnosticUntilOwnerFinishes(t *testing.T) {
	logger := &deferredTimingGateLogger{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(logger.release) }) }
	t.Cleanup(release)
	middleware := NewRequestLoggerWithOptions(CaptureOptions{Bodies: true, SSEFrames: true, EventTiming: true})
	middleware.logger = logger
	var closes atomic.Int32
	payload := "data: {\"input\":\"synthetic-private-deferred\"}\n\n"
	body := strings.NewReader(payload)
	response := fullCaptureResponse(t, middleware, "text/event-stream", &timingTestBody{
		read:  body.Read,
		close: func() error { closes.Add(1); return nil },
	})
	finish := DeferResponseBodyTiming(response)
	completed := make(chan error, 1)
	go func() {
		data, err := io.ReadAll(response.Body)
		if err == nil && string(data) != payload {
			err = errors.New("response bytes changed")
		}
		completed <- errors.Join(err, response.Body.Close())
	}()
	select {
	case err := <-completed:
		require.NoError(t, err)
	case <-logger.entered:
		release()
		<-completed
		t.Fatal("Read or Close reached the deferred diagnostic sink")
	case <-time.After(2 * time.Second):
		t.Fatal("body operation failed to return")
	}
	require.Equal(t, int32(1), closes.Load())
	finished := make(chan struct{})
	go func() { finish(); close(finished) }()
	select {
	case <-logger.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Finish did not reach the diagnostic sink")
	}
	second := make(chan error, 1)
	go func() { finish(); second <- response.Body.Close() }()
	select {
	case err := <-second:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Finish retained bookkeeping locks or close ownership")
	}
	release()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Finish did not return after releasing the sink")
	}
	for _, label := range []string{"response body (", "SSE event 1 ", "SSE frame 1 "} {
		require.Equal(t, 1, strings.Count(logger.output.String(), label), label)
	}
	require.NotContains(t, logger.output.String(), "synthetic-private-deferred")
	require.Equal(t, int32(2), closes.Load())
}

func TestFullCaptureConcurrentClosePreservesLateReadWithoutFalseCompletion(t *testing.T) {
	entered := make(chan struct{})
	closed := make(chan struct{})
	var closeOnce sync.Once
	payload := `{"input":"synthetic-private-late-response"}`
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true, SSEFrames: true, EventTiming: true})
	response := fullCaptureResponse(t, middleware, "application/json", &timingTestBody{
		read: func(p []byte) (int, error) {
			close(entered)
			<-closed
			return copy(p, payload), io.EOF
		},
		close: func() error { closeOnce.Do(func() { close(closed) }); return nil },
	})
	t.Cleanup(func() { closeOnce.Do(func() { close(closed) }) })
	finish := DeferResponseBodyTiming(response)
	type readResult struct {
		data string
		err  error
	}
	completed := make(chan readResult, 1)
	go func() {
		buffer := make([]byte, 256)
		n, err := response.Body.Read(buffer)
		completed <- readResult{data: string(buffer[:n]), err: err}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("underlying Read did not start")
	}
	require.NoError(t, response.Body.Close())
	select {
	case result := <-completed:
		require.Equal(t, payload, result.data)
		require.True(t, result.err == io.EOF)
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock the underlying Read")
	}
	finish()
	require.NotContains(t, output.String(), "synthetic-private-late-response")
	require.Equal(t, 1, strings.Count(output.String(), "response body ("))
	// Either reader or closer can acquire the bookkeeping lock first.
	// Only that observed terminal boundary may produce the final diagnostic.
	ends := strings.Count(output.String(), "response body fully consumed") + strings.Count(output.String(), "response body closed before EOF")
	require.Equal(t, 1, ends)
}

func TestFullCaptureRequestCompletionUsesDeclaredLengthWithoutEOF(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		known    bool
		wantRead string
	}{
		{name: "declared length consumed", known: true, wantRead: "complete read"},
		{name: "unknown length remains partial", wantRead: "partial read"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			payload := "{\"stream\":true,\"input\":\"synthetic-private-no-eof\"}"
			request := httptest.NewRequest(http.MethodPost, "https://example.test/v1/responses", nil)
			request.Header.Set("Content-Type", "application/json")
			request.ContentLength = -1
			if fixture.known {
				request.ContentLength = int64(len(payload))
			}
			var reads int
			request.Body = &timingTestBody{
				read: func(p []byte) (int, error) {
					reads++
					if reads != 1 {
						return 0, io.EOF
					}
					return copy(p, payload), nil
				},
				close: func() error { return nil },
			}
			middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
			_, err := middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
				buffer := make([]byte, len(payload))
				n, readErr := io.ReadFull(got.Body, buffer)
				require.NoError(t, readErr)
				require.Equal(t, payload, string(buffer[:n]))
				require.Equal(t, 1, reads, "the transport deliberately stops before an EOF read")
				return timingResponse(nil), got.Body.Close()
			})
			require.NoError(t, err)
			require.Equal(t, 1, reads, "the observer must not read ahead to obtain EOF")
			require.Contains(t, output.String(), fmt.Sprintf("%d bytes observed; %s", len(payload), fixture.wantRead))
			require.NotContains(t, output.String(), "synthetic-private-no-eof")
		})
	}
}

func TestFullCaptureMIMEPolicyKeepsBodyBytes(t *testing.T) {
	for _, fixture := range []struct {
		name, contentType string
		json              bool
	}{
		{name: "vendor JSON", contentType: "application/vnd.openai+json; charset=utf-8", json: true},
		{name: "JSON", contentType: "application/json", json: true},
		{name: "missing content type"},
		{name: "plain text containing JSON", contentType: "text/plain"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			payload := "{\"stream\":true,\"input\":\"synthetic-private-mime\"}"
			middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true, SSEFrames: true, EventTiming: true})
			response := fullCaptureResponse(t, middleware, fixture.contentType, io.NopCloser(strings.NewReader(payload)))
			got, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, payload, string(got))
			require.NoError(t, response.Body.Close())
			if fixture.json {
				require.Contains(t, output.String(), "\"stream\":true")
				require.NotContains(t, output.String(), "non-JSON body")
			} else {
				require.Contains(t, output.String(), "<omitted: non-JSON body>")
				require.NotContains(t, output.String(), "\"stream\":true")
			}
			require.NotContains(t, output.String(), "SSE frame ")
			require.NotContains(t, output.String(), "complete frame observed")
			require.NotContains(t, output.String(), "synthetic-private-mime")
		})
	}
}

func TestFullCaptureConcurrentRequestsKeepAttemptStateIsolated(t *testing.T) {
	const count = 16
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true, SSEFrames: true, EventTiming: true})
	ready := make(chan struct{}, count)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	type result struct {
		index   int
		attempt uint64
		data    string
		err     error
	}
	completed := make(chan result, count)
	for index := 1; index <= count; index++ {
		go func() {
			requestPayload := fmt.Sprintf("{\"max_tokens\":%d,\"input\":\"synthetic-private-request-%d\"}", index, index)
			responsePayload := fmt.Sprintf("data: {\"sequence_number\":%d,\"delta\":\"synthetic-private-response-%d\"}\n\n", index, index)
			request, err := http.NewRequest(http.MethodPost, "https://example.test/v1/responses", strings.NewReader(requestPayload))
			if err != nil {
				completed <- result{index: index, err: err}
				return
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
				incoming, readErr := io.ReadAll(got.Body)
				closeErr := got.Body.Close()
				if readErr != nil || closeErr != nil {
					return nil, errors.Join(readErr, closeErr)
				}
				if string(incoming) != requestPayload {
					return nil, errors.New("request payload crossed attempt boundaries")
				}
				ready <- struct{}{}
				<-release
				response := timingResponse(io.NopCloser(strings.NewReader(responsePayload)))
				response.Header.Set("Content-Type", "text/event-stream")
				return response, nil
			})
			if err != nil {
				completed <- result{index: index, err: err}
				return
			}
			timing := response.Body.(*timedResponseBody).responseTiming
			data, readErr := io.ReadAll(response.Body)
			completed <- result{
				index: index, attempt: timing.attempt, data: string(data),
				err: errors.Join(readErr, response.Body.Close()),
			}
		}()
	}
	for range count {
		select {
		case <-ready:
		case failed := <-completed:
			t.Fatalf("request failed before the concurrency barrier: index=%d err=%v", failed.index, failed.err)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent requests did not reach the transport barrier")
		}
	}
	unblock()
	results := make([]result, 0, count)
	for range count {
		select {
		case got := <-completed:
			require.NoError(t, got.err)
			results = append(results, got)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent request did not finish")
		}
	}
	seen := make(map[uint64]bool, count)
	logs := output.String()
	for _, got := range results {
		require.False(t, seen[got.attempt], "each middleware invocation needs its own attempt identity")
		seen[got.attempt] = true
		want := fmt.Sprintf("data: {\"sequence_number\":%d,\"delta\":\"synthetic-private-response-%d\"}\n\n", got.index, got.index)
		require.Equal(t, want, got.data)
		var attemptLines []string
		prefix := fmt.Sprintf("HTTP attempt %d: ", got.attempt)
		for _, line := range strings.Split(logs, "\n") {
			if strings.HasPrefix(line, prefix) {
				attemptLines = append(attemptLines, line)
			}
		}
		attemptLogs := strings.Join(attemptLines, "\n")
		require.Contains(t, attemptLogs, fmt.Sprintf("\"max_tokens\":%d,", got.index))
		require.Contains(t, attemptLogs, fmt.Sprintf("\"sequence_number\":%d,", got.index))
		require.Equal(t, 1, strings.Count(attemptLogs, "request body ("))
		require.Equal(t, 1, strings.Count(attemptLogs, "response body ("))
		require.Equal(t, 1, strings.Count(attemptLogs, "SSE frame 1 "))
		require.Equal(t, 1, strings.Count(attemptLogs, "SSE event 1 complete frame observed"))
		require.NotContains(t, attemptLogs, "SSE event 2 ")
	}
	require.NotContains(t, logs, "synthetic-private")
}
