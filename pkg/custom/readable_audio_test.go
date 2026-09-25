package custom

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCaptureAudioTextKeepsBytesAndBodyOwnership(t *testing.T) {
	for _, contentType := range []string{"text/plain; charset=utf-8", "text/srt", "text/vtt", "application/x-subrip"} {
		t.Run(contentType, func(t *testing.T) {
			body := &audioCloseReader{Reader: strings.NewReader("WEBVTT\r\n\r\n00:00:00.000 --> 00:00:01.000\r\nHello\r\n")}
			capture := &audioTextBody{}
			ctx := context.WithValue(t.Context(), audioTextKey{}, capture)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/audio/transcriptions", nil)
			require.NoError(t, err)
			response, err := captureAudioText(request, func(*http.Request) (*http.Response, error) {
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
			response, err := captureAudioText(request, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}, Body: body}, tc.err
			})
			require.ErrorIs(t, err, tc.err)
			require.Same(t, body, response.Body)
			require.False(t, capture.present)
			require.Empty(t, capture.String())
		})
	}
}

func TestAudioTextPresentationFormatsAndCancellation(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"auto", "Spoken words\n"}, {"text", "Spoken words\n"},
		{"raw", "Spoken words"}, {"json", "\"Spoken words\"\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			capture := &audioTextBody{present: true}
			capture.WriteString("Spoken words")
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), audioTextKey{}, capture))
			defer cancel()
			var out strings.Builder
			opts := ShowJSONOpts{Context: ctx, Operation: "(resource) audio.transcriptions > (method) create", OutputKind: OutputResponse, Stdout: &out, Format: tc.format, ExplicitFormat: true}
			require.NoError(t, ShowJSON(gjson.Result{}, opts))
			require.Equal(t, tc.want, out.String())
			out.Reset()
			cancel()
			require.ErrorIs(t, ShowJSON(gjson.Result{}, opts), context.Canceled)
			require.Empty(t, out.String())
		})
	}
}

func TestAudioTextRawReportsShortWrite(t *testing.T) {
	capture := &audioTextBody{present: true}
	capture.WriteString("Spoken words")
	ctx := context.WithValue(t.Context(), audioTextKey{}, capture)
	err := ShowJSON(gjson.Result{}, ShowJSONOpts{Context: ctx, Operation: "(resource) audio.translations > (method) create", OutputKind: OutputResponse, Stdout: shortDownloadWriter{}, Format: "raw", ExplicitFormat: true})
	require.ErrorIs(t, err, io.ErrShortWrite)
}

func TestAudioTextPreservesExtractionAndEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		name, input, format, transform, want string
		raw                                  bool
	}{
		{"empty", "", "text", "", "\n", false},
		{"raw extraction", "hello", "raw", "@tostr", "\"\\\"hello\\\"\"\n", false},
		{"raw-output extraction", "hello", "text", "@tostr", "\"hello\"\n", true},
		{"controls", "line one\n\x1b[31mline two\x1b[0m", "text", "", "line one\n\\u001b[31mline two\\u001b[0m\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &audioTextBody{present: true}
			capture.WriteString(tc.input)
			ctx := context.WithValue(t.Context(), audioTextKey{}, capture)
			var out strings.Builder
			err := ShowJSON(gjson.Result{}, ShowJSONOpts{Context: ctx, Operation: "(resource) audio.transcriptions > (method) create", OutputKind: OutputResponse, Stdout: &out, Format: tc.format, ExplicitFormat: true, RawOutput: tc.raw, Transform: tc.transform})
			require.NoError(t, err)
			require.Equal(t, tc.want, out.String())
		})
	}
}

type audioCloseReader struct {
	io.Reader
	closed bool
}

func (r *audioCloseReader) Close() error { r.closed = true; return nil }
