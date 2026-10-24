package debugmiddleware

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestCaptureRedirectReplay(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, early := range []bool{true, false} {
			t.Run(fmt.Sprintf("status_%d/early_%t", status, early), func(t *testing.T) {
				const payload = `{"input":"synthetic-private-replay","stream":true,"max_output_tokens":8}`
				type received struct {
					path, body, expect string
					err                error
				}
				requests := make(chan received, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					item := received{path: r.URL.Path, expect: r.Header.Get("Expect")}
					if r.URL.Path != "/first" || !early {
						var data []byte
						data, item.err = io.ReadAll(r.Body)
						item.body = string(data)
					}
					requests <- item
					if r.URL.Path == "/first" {
						w.Header().Set("Location", "/second")
						w.Header().Set("Connection", "close")
						w.WriteHeader(status)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				}))
				t.Cleanup(server.Close)
				transport := &http.Transport{ExpectContinueTimeout: 10 * time.Second, DisableKeepAlives: true}
				t.Cleanup(transport.CloseIdleConnections)
				client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
				request, err := http.NewRequest(http.MethodPost, server.URL+"/first", strings.NewReader(payload))
				require.NoError(t, err)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer synthetic-replay-key")
				if early {
					request.Header.Set("Expect", "100-continue")
				}
				originalBody, originalGetBody := request.Body, request.GetBody
				var originalReads, replayReads, replayCalls atomic.Int32
				tracked := &timingTestBody{
					read:  func(p []byte) (int, error) { originalReads.Add(1); return originalBody.Read(p) },
					close: originalBody.Close,
				}
				request.Body = tracked
				request.GetBody = func() (io.ReadCloser, error) {
					replayCalls.Add(1)
					body, bodyErr := originalGetBody()
					if bodyErr != nil {
						return nil, bodyErr
					}
					return &timingTestBody{
						read:  func(p []byte) (int, error) { replayReads.Add(1); return body.Read(p) },
						close: body.Close,
					}, nil
				}
				middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
				response, err := middleware.Middleware()(request, client.Do)
				require.NoError(t, err)
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, `{"ok":true}`, string(body))
				require.Same(t, tracked, request.Body)
				require.Equal(t, "/second", response.Request.URL.Path)
				require.Equal(t, int32(1), replayCalls.Load())
				require.Positive(t, replayReads.Load())
				if early {
					require.Zero(t, originalReads.Load(), "the redirect must arrive before the initial upload")
				} else {
					require.Positive(t, originalReads.Load())
				}
				for i, path := range []string{"/first", "/second"} {
					select {
					case got := <-requests:
						require.NoError(t, got.err)
						require.Equal(t, path, got.path)
						if i == 0 && early {
							require.Equal(t, "100-continue", got.expect)
							require.Empty(t, got.body)
						} else {
							require.Equal(t, payload, got.body)
						}
					default:
						t.Fatalf("missing request %s", path)
					}
				}
				logs := output.String()
				require.Equal(t, 1, strings.Count(logs, "request body ("), "replays must not duplicate the snapshot")
				require.Contains(t, logs, fmt.Sprintf("request body (redacted; %d bytes observed; complete read)", len(payload)))
				require.Contains(t, logs, `"input":"<REDACTED>"`)
				require.Contains(t, logs, `"stream":true`)
				require.Contains(t, logs, `"max_output_tokens":8`)
				require.NotContains(t, logs, "synthetic-private-replay")
				require.NotContains(t, logs, "synthetic-replay-key")
			})
		}
	}
}

func TestRequestCaptureReplayPreservesErrorsAndOwnership(t *testing.T) {
	t.Run("factory_error", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "https://example.test/replay", strings.NewReader(`{"input":"synthetic-private"}`))
		request.Header.Set("Content-Type", "application/json")
		failure := errors.New("synthetic-private-replay-factory-error")
		var calls int
		request.GetBody = func() (io.ReadCloser, error) { calls++; return nil, failure }
		middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
		_, err := middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
			require.Zero(t, calls, "capture must not request a replay before the transport")
			body, replayErr := got.GetBody()
			require.Nil(t, body)
			require.Same(t, failure, replayErr)
			return nil, replayErr
		})
		require.Same(t, failure, err)
		require.Equal(t, 1, calls)
		require.NotContains(t, output.String(), failure.Error())
		require.NoError(t, request.Body.Close())
	})
	for _, terminal := range []error{io.EOF, errors.New("synthetic-private-replay-read-error")} {
		t.Run(terminal.Error(), func(t *testing.T) {
			const payload = `{"input":"synthetic-private-replay","stream":true}`
			request := httptest.NewRequest(http.MethodPost, "https://example.test/replay", nil)
			request.Header.Set("Content-Type", "application/json")
			request.ContentLength = -1
			closeFailure := errors.New("synthetic-private-replay-close-error")
			var originalReads, originalCloses, replayReads, replayCloses, factoryCalls int
			original := &timingTestBody{
				read:  func([]byte) (int, error) { originalReads++; return 0, io.EOF },
				close: func() error { originalCloses++; return nil },
			}
			request.Body = original
			request.GetBody = func() (io.ReadCloser, error) {
				factoryCalls++
				return &timingTestBody{
					read:  func(p []byte) (int, error) { replayReads++; return copy(p, payload), terminal },
					close: func() error { replayCloses++; return closeFailure },
				}, nil
			}
			middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
			_, err := middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
				require.Zero(t, factoryCalls)
				replay, replayErr := got.GetBody()
				require.NoError(t, replayErr)
				require.Zero(t, replayReads, "creating a replay must not consume it")
				buffer := make([]byte, len(payload)+1)
				n, readErr := replay.Read(buffer)
				require.Equal(t, payload, string(buffer[:n]))
				require.Same(t, terminal, readErr)
				require.Same(t, closeFailure, replay.Close())
				require.Equal(t, 1, replayCloses)
				return nil, closeFailure
			})
			require.Same(t, closeFailure, err)
			require.Same(t, original, request.Body)
			require.Zero(t, originalReads)
			require.Zero(t, originalCloses)
			require.Equal(t, 1, factoryCalls)
			require.Equal(t, 1, replayReads)
			require.Equal(t, 1, replayCloses, "middleware must not close the replay again")
			state := "partial read"
			if terminal == io.EOF {
				state = "complete read"
			}
			logs := output.String()
			require.Equal(t, 1, strings.Count(logs, "request body ("))
			require.Contains(t, logs, fmt.Sprintf("request body (redacted; %d bytes observed; %s)", len(payload), state))
			require.NotContains(t, logs, "synthetic-private")
			require.NoError(t, request.Body.Close())
			require.Equal(t, 1, originalCloses)
		})
	}
}

func TestRequestCaptureUnusedReplayPreservesSnapshot(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(fmt.Sprintf("check_redirect_%t", redirect), func(t *testing.T) {
			const payload = `{"input":"synthetic-private-original","max_output_tokens":8}`
			request, err := http.NewRequest(http.MethodPost, "https://example.test/first", strings.NewReader(payload))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/json")
			var factories, replayReads int
			request.GetBody = func() (io.ReadCloser, error) {
				factories++
				reader := strings.NewReader(payload)
				return &timingTestBody{
					read:  func(p []byte) (int, error) { replayReads++; return reader.Read(p) },
					close: func() error { return nil },
				}, nil
			}
			consume := func(got *http.Request) (*http.Response, error) {
				data, readErr := io.ReadAll(got.Body)
				require.NoError(t, readErr)
				require.Equal(t, payload, string(data))
				require.NoError(t, got.Body.Close())
				response := timingResponse(http.NoBody)
				response.Request = got
				if redirect {
					response.StatusCode = http.StatusTemporaryRedirect
					response.Header.Set("Location", "/second")
				} else {
					replay, replayErr := got.GetBody()
					require.NoError(t, replayErr)
					require.NoError(t, replay.Close())
				}
				return response, nil
			}
			next := MiddlewareNext(consume)
			if redirect {
				client := &http.Client{
					Transport: roundTripFunc(consume),
					CheckRedirect: func(got *http.Request, via []*http.Request) error {
						require.Equal(t, "/second", got.URL.Path)
						require.Len(t, via, 1)
						return http.ErrUseLastResponse
					},
				}
				next = client.Do
			}
			middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
			response, err := middleware.Middleware()(request, next)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, 1, factories)
			require.Zero(t, replayReads)
			logs := output.String()
			require.Equal(t, 1, strings.Count(logs, "request body ("))
			require.Contains(t, logs, fmt.Sprintf("request body (redacted; %d bytes observed; complete read)", len(payload)))
			require.Contains(t, logs, `"max_output_tokens":8`)
			require.NotContains(t, logs, "synthetic-private-original")
		})
	}
}

func TestRequestCaptureIgnoresOlderAndFinishedReplayReads(t *testing.T) {
	const originalPayload = `{"input":"synthetic-private-original","max_output_tokens":1}`
	const olderPayload = `{"input":"synthetic-private-older-replay","max_output_tokens":333}`
	const replayPayload = `{"input":"synthetic-private-replay","max_output_tokens":22}`
	request, err := http.NewRequest(http.MethodPost, "https://example.test/replay", strings.NewReader(originalPayload))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.ContentLength = -1
	var factoryCalls int
	request.GetBody = func() (io.ReadCloser, error) {
		factoryCalls++
		if factoryCalls == 1 {
			return io.NopCloser(strings.NewReader(olderPayload)), nil
		}
		return io.NopCloser(strings.NewReader(replayPayload)), nil
	}
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
	var captured *http.Request
	_, err = middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
		captured = got
		prefix := make([]byte, 4)
		_, readErr := io.ReadFull(got.Body, prefix)
		require.NoError(t, readErr)
		olderReplay, replayErr := got.GetBody()
		require.NoError(t, replayErr)
		newerReplay, replayErr := got.GetBody()
		require.NoError(t, replayErr)
		newer, readErr := io.ReadAll(newerReplay)
		require.NoError(t, readErr)
		require.Equal(t, replayPayload, string(newer))
		require.NoError(t, newerReplay.Close())
		older, readErr := io.ReadAll(olderReplay)
		require.NoError(t, readErr)
		require.Equal(t, olderPayload, string(older))
		require.NoError(t, olderReplay.Close())
		rest, readErr := io.ReadAll(got.Body)
		require.NoError(t, readErr)
		require.Equal(t, originalPayload, string(prefix)+string(rest))
		require.NoError(t, got.Body.Close())
		return timingResponse(nil), nil
	})
	require.NoError(t, err)
	logs := output.String()
	require.Equal(t, 1, strings.Count(logs, "request body ("))
	require.Contains(t, logs, fmt.Sprintf("request body (redacted; %d bytes observed; complete read)", len(replayPayload)))
	require.Contains(t, logs, `"max_output_tokens":22`)
	require.NotContains(t, logs, `"max_output_tokens":1`)
	require.NotContains(t, logs, `"max_output_tokens":333`)
	require.NotContains(t, logs, "synthetic-private")
	lateReplay, err := captured.GetBody()
	require.NoError(t, err)
	late, err := io.ReadAll(lateReplay)
	require.NoError(t, err)
	require.Equal(t, replayPayload, string(late))
	require.NoError(t, lateReplay.Close())
	require.Equal(t, logs, output.String(), "replay reads after finish must not emit diagnostics")
}

func TestRequestCaptureRepeatedReplayRemainsBounded(t *testing.T) {
	const limit = 128
	payload := `{"input":"synthetic-private-` + strings.Repeat("x", 8*1024) + `","stream":true}`
	request, err := http.NewRequest(http.MethodPost, "https://example.test/replay", strings.NewReader(payload))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true, MaxBytes: limit})
	_, err = middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
		for range 64 {
			replay, replayErr := got.GetBody()
			require.NoError(t, replayErr)
			data, readErr := io.ReadAll(replay)
			require.NoError(t, readErr)
			require.Equal(t, payload, string(data), "capture limits must not limit replay payloads")
			require.NoError(t, replay.Close())
		}
		require.NoError(t, got.Body.Close())
		return timingResponse(nil), nil
	})
	require.NoError(t, err)
	logs := output.String()
	require.Equal(t, 1, strings.Count(logs, "request body ("))
	require.Contains(t, logs, fmt.Sprintf("request body (redacted; %d bytes observed; complete read, capture truncated)", len(payload)))
	require.NotContains(t, logs, "synthetic-private")
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "request body (") {
			require.Less(t, len(line), limit+512, "diagnostic output must remain bounded across repeated replay")
		}
	}
}

func TestRequestCaptureReplayPreservesEmptyBodyIdentity(t *testing.T) {
	for _, body := range []io.ReadCloser{nil, http.NoBody} {
		t.Run(fmt.Sprintf("no_body_%t", body == http.NoBody), func(t *testing.T) {
			const payload = `{"max_output_tokens":8}`
			request, err := http.NewRequest(http.MethodPost, "https://example.test/replay", strings.NewReader(payload))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/json")
			request.GetBody = func() (io.ReadCloser, error) { return body, nil }
			middleware, output := fullCaptureLogger(CaptureOptions{Bodies: true})
			_, err = middleware.Middleware()(request, func(got *http.Request) (*http.Response, error) {
				data, readErr := io.ReadAll(got.Body)
				require.NoError(t, readErr)
				require.Equal(t, payload, string(data))
				require.NoError(t, got.Body.Close())
				replay, replayErr := got.GetBody()
				require.NoError(t, replayErr)
				require.True(t, replay == body, "nil and http.NoBody identities must remain unchanged")
				return timingResponse(nil), nil
			})
			require.NoError(t, err)
			require.Contains(t, output.String(), fmt.Sprintf("request body (redacted; %d bytes observed; complete read)", len(payload)))
			require.Contains(t, output.String(), `"max_output_tokens":8`)
		})
	}
}
