package jsonview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type failedExplorerStream struct {
	explorerIterator
	err error
}

func (it *failedExplorerStream) Err() error { return it.err }

func TestExplorerPreloadFailurePreservesCompleteEscapedJSON(t *testing.T) {
	failure := errors.New("synthetic stream failure")
	first := json.RawMessage("{\r\n\"text\":\"first\u009b31m\",\"large_integer\":9007199254740993}")
	second := json.RawMessage(`{"type":"error","detail":{"future":true}}`)
	iter := &failedExplorerStream{explorerIterator{items: []any{first, second}}, failure}
	var out bytes.Buffer
	err := ExploreJSONStreamWithOutput("synthetic", iter, &out)
	require.ErrorIs(t, err, failure)
	require.JSONEq(t, "["+string(first)+","+string(second)+"]", out.String())
	require.Contains(t, out.String(), "9007199254740993")
	require.NotContains(t, out.String(), "\u009b")
	require.Contains(t, out.String(), `\u009b`)
	require.Equal(t, 3, iter.index, "one read observes EOF; no extra records consumed")
}

type failedExplorerSink struct{ err error }

func (w failedExplorerSink) Write([]byte) (int, error) { return 0, w.err }

func TestExplorerPreloadFailureRetainsSinkErrorsAndCancellation(t *testing.T) {
	failure := errors.New("synthetic upstream failure")
	sink := errors.New("synthetic sink failure")
	for _, sinkErr := range []error{sink, nil} {
		iter := &failedExplorerStream{explorerIterator{items: []any{json.RawMessage(`{"partial":true}`)}}, failure}
		err := ExploreJSONStreamWithOutput("synthetic", iter, failedExplorerSink{sinkErr})
		require.ErrorIs(t, err, failure)
		if sinkErr == nil {
			sinkErr = io.ErrShortWrite
		}
		require.ErrorIs(t, err, sinkErr)
	}
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		iter := &failedExplorerStream{explorerIterator{items: []any{json.RawMessage(`{"buffered":true}`)}}, failure}
		var out bytes.Buffer
		require.ErrorIs(t, ExploreJSONStreamWithOutput("synthetic", iter, &out), failure)
		require.Empty(t, out.String(), "cancellation must not flush preloaded values")
	}
	var out bytes.Buffer
	require.ErrorIs(t, ExploreJSONStreamWithOutput("synthetic", &failedExplorerStream{err: failure}, &out), failure)
	require.Empty(t, out.String())
}
