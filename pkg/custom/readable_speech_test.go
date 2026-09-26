package custom

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpeechEventIteratorPreservesFramingAndValues(t *testing.T) {
	const value = `{"type":"speech.audio.done","usage":{"input_tokens":9007199254740993}}`
	for _, newline := range []string{"\n", "\r", "\r\n"} {
		for _, suffix := range []string{"", newline + newline + "data: [DONE]" + newline + newline + `data: {"after_done":true}`} {
			wire := "\ufeff: heartbeat" + newline + "event: speech.audio.done" + newline + "id: synthetic-event" + newline + "data: " + value + suffix
			iter := &speechEventIterator{reader: bufio.NewReaderSize(strings.NewReader(wire), 16)}
			require.True(t, iter.Next(), "framing %q: %v", newline, iter.Err())
			require.Equal(t, value, iter.Current().RawJSON())
			require.False(t, iter.Next())
			require.NoError(t, iter.Err())
		}
	}
	iter := &speechEventIterator{reader: bufio.NewReader(strings.NewReader("data: {\"type\":\"future\",\ndata: \"precise\":9007199254740993}\n\n"))}
	require.True(t, iter.Next())
	require.Equal(t, "9007199254740993", iter.Current().Get("precise").Raw)
}

func TestSpeechEventIteratorNamesApplyToOneEvent(t *testing.T) {
	const value = `{"type":"speech.audio.done","event":"error"}`
	for _, prefix := range []string{
		"event: error\n\n",
		"event: error\nevent: speech.audio.done\n",
		"event: error\nevent\n",
		"",
	} {
		iter := &speechEventIterator{reader: bufio.NewReader(strings.NewReader(prefix + "data: " + value + "\n\n"))}
		require.True(t, iter.Next())
		require.Equal(t, value, iter.Current().RawJSON())
		require.False(t, iter.Next())
		require.NoError(t, iter.Err())
	}
}

func TestSpeechEventIteratorNamedErrorStopsAfterOriginalEvent(t *testing.T) {
	const value = `{"message":"Synthetic failure","precise":9007199254740993}`
	iter := &speechEventIterator{reader: bufio.NewReader(strings.NewReader("event: error\ndata: " + value + "\n\ndata: {\"must_not_read\":true}\n\n"))}
	require.True(t, iter.Next())
	require.Equal(t, value, iter.Current().RawJSON())
	require.EqualError(t, iter.Err(), "the API reported an error while streaming")
	require.False(t, iter.Next())
	require.Equal(t, value, iter.Current().RawJSON())
}

func TestSpeechEventIteratorReadAndParseFailures(t *testing.T) {
	want := errors.New("synthetic read failure")
	iter := &speechEventIterator{reader: bufio.NewReader(&errorAfterDownloadReader{Reader: strings.NewReader("data: {"), err: want})}
	require.False(t, iter.Next())
	require.ErrorIs(t, iter.Err(), want)
	iter = &speechEventIterator{reader: bufio.NewReader(strings.NewReader("data: synthetic-private-invalid-json\n\n"))}
	require.False(t, iter.Next())
	require.EqualError(t, iter.Err(), "could not read a speech stream event as JSON")
	var classified *streamResultError
	require.ErrorAs(t, iter.Err(), &classified)
}

func TestSpeechEventIteratorRetainsCompleteFinalValueWithReadFailure(t *testing.T) {
	const value = `{"type":"error","message":"synthetic failure"}`
	want := errors.New("synthetic read failure")
	iter := &speechEventIterator{reader: bufio.NewReader(&errorAfterDownloadReader{Reader: strings.NewReader("data: " + value), err: want})}
	require.True(t, iter.Next())
	require.Equal(t, value, iter.Current().RawJSON())
	require.ErrorIs(t, iter.Err(), want)
	require.False(t, iter.Next())
}

func TestSpeechOutputPreservesExactRawBytesAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name, events, wantError string
	}{
		{"success", `data: {"type":"speech.audio.done","future":9007199254740993}` + "\r\n\r\n", ""},
		{"typed failure", `data: {"type":"error","message":"synthetic-private"}` + "\r\n\r\n", "the API reported an error while streaming"},
		{"named failure", "event: error\r\n" + `data: {"message":"synthetic-private"}` + "\r\n\r\n", "the API reported an error while streaming"},
		{"unfinished", `data: {"type":"speech.audio.delta","audio":"YQ=="}` + "\r\n\r\n", "the stream ended before the speech audio completed"},
		{"malformed", "data: synthetic-private-invalid-json\r\n\r\n", "could not read a speech stream event as JSON"},
	} {
		for _, mode := range []string{"raw", "RAW", "raw-output", "stdout", "dev-stdout", "file"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				wire := "\ufeff: synthetic heartbeat\r\nid: synthetic-event\r\n" + tc.events + "data: [DONE]\r\n\r\ntrailing bytes\r\n"
				opts := ShowJSONOpts{Format: "json", ExplicitFormat: true}
				outfile := ""
				switch mode {
				case "raw", "RAW":
					opts.Format = mode
				case "raw-output":
					opts.RawOutput = true
				case "stdout":
					outfile = "-"
				case "dev-stdout":
					outfile = "/dev/stdout"
				case "file":
					outfile = filepath.Join(t.TempDir(), "speech.sse")
				}
				response, body := testSpeechResponse(t, t.Context(), wire, opts)
				var out bytes.Buffer
				message, err := WriteBinaryResponse(response, &out, outfile)
				if tc.wantError == "" {
					require.NoError(t, err)
				} else {
					require.EqualError(t, err, tc.wantError)
					require.Empty(t, message)
				}
				require.True(t, body.closed)
				if mode == "file" {
					data, err := os.ReadFile(outfile)
					require.NoError(t, err)
					require.Equal(t, wire, string(data))
					require.Empty(t, out.String())
					if tc.wantError == "" {
						require.Equal(t, "Wrote output to: "+outfile, message)
					}
				} else {
					require.Equal(t, wire, out.String())
					require.Empty(t, message)
				}
			})
		}
	}
}

func TestSpeechJSONAndExtractionKeepOriginalValuesAndFailure(t *testing.T) {
	const wire = "data: {\"type\":\"speech.audio.delta\",\"audio\":\"YQ==\",\"future\":9007199254740993}\n\nevent: error\ndata: {\"message\":\"synthetic failure\"}\n\ndata: {\"must_not_read\":true}\n\n"
	for _, format := range []string{"json", "jsonl", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			response, body := testSpeechResponse(t, t.Context(), wire, ShowJSONOpts{Format: format, ExplicitFormat: true, Stderr: io.Discard})
			var out bytes.Buffer
			_, err := WriteBinaryResponse(response, &out, "")
			require.EqualError(t, err, "the API reported an error while streaming")
			require.True(t, body.closed)
			require.Contains(t, out.String(), "YQ==")
			require.Contains(t, out.String(), "9007199254740993")
			require.Contains(t, out.String(), "synthetic failure")
			require.NotContains(t, out.String(), "must_not_read")
		})
	}
	response, body := testSpeechResponse(t, t.Context(), "event: error\ndata: {\"message\":\"synthetic failure\"}\n\n", ShowJSONOpts{Transform: "message", RawOutput: true})
	var out bytes.Buffer
	_, err := WriteBinaryResponse(response, &out, "")
	require.EqualError(t, err, "the API reported an error while streaming")
	require.Equal(t, "synthetic failure\n", out.String())
	require.True(t, body.closed)
}

func TestSpeechRawFormatHonorsExtraction(t *testing.T) {
	const wire = "data: {\"type\":\"speech.audio.delta\",\"audio\":\"YQ==\"}\n\ndata: {\"type\":\"speech.audio.done\"}\n\n"
	response, body := testSpeechResponse(t, t.Context(), wire, ShowJSONOpts{Format: "raw", Transform: "type", ExplicitFormat: true})
	var out bytes.Buffer
	_, err := WriteBinaryResponse(response, &out, "")
	require.NoError(t, err)
	require.Equal(t, "\"speech.audio.delta\"\n\"speech.audio.done\"\n", out.String())
	require.True(t, body.closed)
}

func TestSpeechRawFailureStopsBeforeStalledRemainder(t *testing.T) {
	for _, event := range []string{
		"data: {\"type\":\"error\",\"message\":\"synthetic-private\"}\n\n",
		"event: error\ndata: {\"message\":\"synthetic-private\"}\n\n",
		"data: synthetic-private-malformed\n\n",
	} {
		reader, writer := io.Pipe()
		response, _ := testSpeechResponse(t, t.Context(), "", ShowJSONOpts{Format: "raw"})
		response.Body = reader
		var out bytes.Buffer
		done := make(chan error, 1)
		go func() {
			_, err := WriteBinaryResponse(response, &out, "")
			done <- err
		}()
		_, writeErr := io.WriteString(writer, event)
		require.NoError(t, writeErr)
		select {
		case err := <-done:
			require.Error(t, err)
			require.NotContains(t, err.Error(), "synthetic-private")
			require.Equal(t, event, out.String())
			_, err = writer.Write([]byte("unread remainder"))
			require.ErrorIs(t, err, io.ErrClosedPipe)
		case <-time.After(time.Second):
			_ = reader.Close()
			_ = writer.Close()
			<-done
			t.Fatal("failed stream kept reading its stalled remainder")
		}
		_ = writer.Close()
	}
}

func TestSpeechStopsOnShortOrClosedWrites(t *testing.T) {
	closed := outputFile(t)
	require.NoError(t, closed.Close())
	for _, tc := range []struct {
		name   string
		writer io.Writer
		err    error
	}{
		{"short", shortDownloadWriter{}, io.ErrShortWrite},
		{"closed", closed, os.ErrClosed},
	} {
		for _, format := range []string{"raw", "json", "text"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				const wire = "data: {\"type\":\"error\",\"message\":\"synthetic-private\"}\n\n"
				response, body := testSpeechResponse(t, t.Context(), wire+strings.Repeat(" ", 1<<20), ShowJSONOpts{Format: format, ExplicitFormat: true})
				_, err := WriteBinaryResponse(response, tc.writer, "")
				require.ErrorIs(t, err, tc.err)
				require.Contains(t, err.Error(), "the API reported an error while streaming")
				require.NotContains(t, err.Error(), "synthetic-private")
				require.LessOrEqual(t, body.read, 4096)
				require.True(t, body.closed)
			})
		}
	}
}

func TestSpeechRawStopsImmediatelyOnUnfinishedEventWriteFailure(t *testing.T) {
	const size = 1 << 20
	reader := &boundedDownloadReader{remaining: size}
	err := copySpeechStream(t.Context(), shortDownloadWriter{}, reader)
	require.ErrorIs(t, err, io.ErrShortWrite)
	require.LessOrEqual(t, size-reader.remaining, int64(4096))
}

func TestSpeechRawRetainsFailureOnClosedFinalUnterminatedEvent(t *testing.T) {
	for _, wire := range []string{
		`data: {"type":"error","message":"synthetic-private"}`,
		"event: error\ndata: {\"message\":\"synthetic-private\"}",
	} {
		response, body := testSpeechResponse(t, t.Context(), wire, ShowJSONOpts{Format: "raw"})
		_, err := WriteBinaryResponse(response, failOutputWriter{errors.New("broken pipe")}, "")
		require.Contains(t, err.Error(), "the API reported an error while streaming")
		require.NotContains(t, err.Error(), "synthetic-private")
		require.False(t, isOutputBrokenPipe(err))
		require.True(t, body.closed)
	}
}

func TestSpeechCancellationAndFailureCloseBodies(t *testing.T) {
	for _, format := range []string{"json", "raw"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, "data: {\"type\":\"speech.audio.delta\",\"audio\":\"YQ==\"}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)
			opts := ShowJSONOpts{Context: ctx, Format: format, ExplicitFormat: true, Operation: speechStreamOperation, OutputKind: OutputStreamEvent}
			request, err := http.NewRequestWithContext(context.WithValue(ctx, speechOutputKey{}, opts), http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			response, err := server.Client().Do(request)
			require.NoError(t, err)
			out := &cancelingDownloadWriter{cancel: cancel}
			_, err = WriteBinaryResponse(response, out, "")
			require.ErrorIs(t, err, context.Canceled)
			require.Positive(t, out.written)
		})
	}
	response, body := testSpeechResponse(t, t.Context(), "data: {}\n\n", ShowJSONOpts{})
	_, err := WriteBinaryResponse(response, io.Discard, filepath.Join(t.TempDir(), "missing", "speech.sse"))
	require.Error(t, err)
	require.True(t, body.closed)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	response, body = testSpeechResponse(t, ctx, "data: {}\n\n", ShowJSONOpts{Format: "raw"})
	_, err = WriteBinaryResponse(response, io.Discard, "")
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, body.read)
	require.True(t, body.closed)
}

func TestSpeechHookLeavesOtherResponsesUntouched(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		status            int
		context           bool
	}{
		{"binary audio", "audio/mpeg", 200, true},
		{"invalid media type", "text/event-stream; broken", 200, true},
		{"HTTP error", "text/event-stream", 400, true},
		{"another endpoint", "text/event-stream", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, body := testSpeechResponse(t, t.Context(), "synthetic bytes", ShowJSONOpts{})
			response.Header.Set("Content-Type", tc.contentType)
			response.StatusCode = tc.status
			if !tc.context {
				response.Request = response.Request.WithContext(t.Context())
			}
			handled, _, err := writeReadableSpeech(response, io.Discard, "")
			require.False(t, handled)
			require.NoError(t, err)
			require.Zero(t, body.read)
		})
	}
}

type speechTrackingBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *speechTrackingBody) Read(data []byte) (int, error) {
	n, err := b.Reader.Read(data)
	b.read += n
	return n, err
}

func (b *speechTrackingBody) Close() error { b.closed = true; return nil }

func testSpeechResponse(t *testing.T, ctx context.Context, wire string, opts ShowJSONOpts) (*http.Response, *speechTrackingBody) {
	t.Helper()
	opts.Context, opts.Operation, opts.OutputKind = ctx, speechStreamOperation, OutputStreamEvent
	request, err := http.NewRequestWithContext(context.WithValue(ctx, speechOutputKey{}, opts), http.MethodGet, "http://synthetic.invalid/v1/audio/speech", nil)
	require.NoError(t, err)
	body := &speechTrackingBody{Reader: strings.NewReader(wire)}
	return &http.Response{Request: request, Body: body, StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}}, body
}
