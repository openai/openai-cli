package audio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureAudioTextKeepsBytesAndBodyOwnership(t *testing.T) {
	for _, contentType := range []string{"text/plain; charset=utf-8", "text/srt", "text/vtt", "application/x-subrip"} {
		t.Run(contentType, func(t *testing.T) {
			body := &audioCloseReader{Reader: strings.NewReader("WEBVTT\r\n\r\n00:00:00.000 --> 00:00:01.000\r\nHello\r\n")}
			capture := &audioTextBody{}
			ctx := context.WithValue(t.Context(), audioTextKey{}, capture)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/audio/transcriptions", nil)
			require.NoError(t, err)
			response, err := CaptureText(request, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: body}, nil
			})
			require.NoError(t, err)
			require.Empty(t, capture.String(), "capture must not read ahead")
			data, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, string(data), capture.String())
			require.True(t, capture.present)
			require.False(t, body.closed)
			require.NoError(t, response.Body.Close())
			require.True(t, body.closed)
		})
	}
}

func TestCaptureAudioTextDoesNotReusePriorAttempt(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		status            int
		err               error
	}{
		{"json", "application/json", 200, nil},
		{"stream", "text/event-stream", 200, nil},
		{"http error", "text/plain", 400, nil},
		{"request error", "text/plain", 200, errors.New("synthetic transport failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &audioTextBody{present: true}
			capture.WriteString("stale attempt")
			ctx := context.WithValue(t.Context(), audioTextKey{}, capture)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost", nil)
			require.NoError(t, err)
			body := &audioCloseReader{Reader: strings.NewReader("synthetic")}
			response, err := CaptureText(request, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}, Body: body}, tc.err
			})
			require.ErrorIs(t, err, tc.err)
			require.Same(t, body, response.Body)
			require.False(t, capture.present)
			require.Empty(t, capture.String())
		})
	}
}

type audioCloseReader struct {
	io.Reader
	closed bool
}

func (r *audioCloseReader) Close() error { r.closed = true; return nil }
