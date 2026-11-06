package audio

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	clioutput "github.com/openai/openai-cli/internal/output"
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
	var classified *clioutput.StreamResultError
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

func TestSpeechRawStopsImmediatelyOnUnfinishedEventWriteFailure(t *testing.T) {
	const size = 1 << 20
	reader := &boundedDownloadReader{remaining: size}
	err := copySpeechStream(t.Context(), shortDownloadWriter{}, reader)
	require.ErrorIs(t, err, io.ErrShortWrite)
	require.LessOrEqual(t, size-reader.remaining, int64(4096))
}

type boundedDownloadReader struct {
	remaining int64
	closed    bool
}

func (reader *boundedDownloadReader) Read(data []byte) (int, error) {
	if len(data) > 64<<10 {
		return 0, fmt.Errorf("download requested an unbounded %d-byte read buffer", len(data))
	}
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(data)) > reader.remaining {
		data = data[:reader.remaining]
	}
	for index := range data {
		data[index] = 'a'
	}
	reader.remaining -= int64(len(data))
	return len(data), nil
}

func (reader *boundedDownloadReader) Close() error {
	reader.closed = true
	return nil
}

type errorAfterDownloadReader struct {
	io.Reader
	err error
}

func (reader *errorAfterDownloadReader) Read(data []byte) (int, error) {
	n, err := reader.Reader.Read(data)
	if errors.Is(err, io.EOF) {
		return n, reader.err
	}
	return n, err
}

type shortDownloadWriter struct{}

func (shortDownloadWriter) Write(data []byte) (int, error) {
	return len(data) - 1, nil
}
