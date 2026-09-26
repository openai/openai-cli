package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type savedImageTestStream struct {
	events []outputJSON
	index  int
	err    error
	closed bool
}

func (s *savedImageTestStream) Next() bool          { s.index++; return s.index <= len(s.events) }
func (s *savedImageTestStream) Current() outputJSON { return s.events[s.index-1] }
func (s *savedImageTestStream) Err() error          { return s.err }
func (s *savedImageTestStream) Close() error        { s.closed = true; return nil }

func TestSaveFinalImageStreamStopsAndClosesAtCompletion(t *testing.T) {
	stream := &savedImageTestStream{events: []outputJSON{
		{gjson.Parse(`{"type":"image_generation.partial_image","b64_json":"ignored"}`)},
		{gjson.Parse(`{"type":"image_generation.completed","b64_json":"iVBORw0KGgo="}`)},
		{gjson.Parse(`{"type":"image_generation.failed"}`)},
	}}
	var output bytes.Buffer
	plan := &imageOutputPlan{directory: t.TempDir(), name: "robot"}
	require.NoError(t, saveFinalImageStream(t.Context(), stream, plan, &output))
	require.Equal(t, 2, stream.index)
	require.True(t, stream.closed)
	files, err := os.ReadDir(plan.directory)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Contains(t, output.String(), "Saved image:")
}

func TestSaveFinalImageStreamFailureAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name      string
		events    []outputJSON
		sourceErr error
	}{
		{name: "EOF"},
		{name: "transport", sourceErr: errors.New("synthetic-private-response")},
		{name: "malformed final", events: []outputJSON{{gjson.Parse(`{"type":"image_generation.completed","b64_json":{"secret":"synthetic-private-response"}}`)}}},
		{name: "failure event", events: []outputJSON{{gjson.Parse(`{"type":"error","message":"synthetic-private-response"}`)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := &savedImageTestStream{events: test.events, err: test.sourceErr}
			err := saveFinalImageStream(t.Context(), stream, &imageOutputPlan{directory: t.TempDir()}, io.Discard)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "synthetic-private-response")
			require.Contains(t, err.Error(), "before trying again")
			require.True(t, stream.closed)
			if test.sourceErr != nil {
				require.ErrorIs(t, err, test.sourceErr)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stream := &savedImageTestStream{}
	require.ErrorIs(t, saveFinalImageStream(ctx, stream, &imageOutputPlan{}, io.Discard), context.Canceled)
	require.Zero(t, stream.index)
	require.True(t, stream.closed)
}

type failingImageWriter struct{ err error }

func (w failingImageWriter) Write([]byte) (int, error) { return 0, w.err }

func TestSavedImageOutputFailureRetainsFilesAndPartialError(t *testing.T) {
	plan := &imageOutputPlan{directory: t.TempDir(), name: "robot"}
	sinkErr := errors.New("closed synthetic sink")
	err := plan.save(t.Context(), []byte(`{"data":[{"b64_json":"iVBORw0KGgo="},{"b64_json":"invalid"}]}`), failingImageWriter{sinkErr})
	require.ErrorIs(t, err, sinkErr)
	require.Contains(t, err.Error(), "could not save the entire response")
	require.Contains(t, err.Error(), "print all saved paths")
	message := localErrorMessage(nil, err)
	require.Contains(t, message, "could not save the entire response")
	require.Contains(t, message, "print all saved paths")
	require.Contains(t, message, "Check the output folder before generating again")
	require.NotContains(t, message, "listed files")
	files, readErr := os.ReadDir(plan.directory)
	require.NoError(t, readErr)
	require.Len(t, files, 1)
}
