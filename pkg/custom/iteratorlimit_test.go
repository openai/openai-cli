package custom

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
)

type countedIterator struct {
	reads int
	err   error
}

func (i *countedIterator) Next() bool   { i.reads++; return i.reads <= 5 && i.err == nil }
func (i *countedIterator) Current() int { return i.reads }
func (i *countedIterator) Err() error   { return i.err }

func TestInteractiveIteratorLimitStopsUpstreamReads(t *testing.T) {
	for _, limit := range []int64{0, 1, 3, -1} {
		upstream := &countedIterator{}
		iter := &outputIterator[int]{source: upstream, context: context.Background(), transform: transformers.Identity, remaining: limit}
		var values []int
		for iter.Next() {
			values = append(values, int(iter.Current().Int()))
		}
		if limit >= 0 {
			require.Len(t, values, int(limit))
			require.Equal(t, int(limit), upstream.reads, "must not fetch a page beyond the requested limit")
			require.False(t, iter.Next())
			require.Equal(t, int(limit), upstream.reads)
		} else {
			require.Equal(t, []int{1, 2, 3, 4, 5}, values)
		}
	}
}

func TestIteratorLimitsPreserveErrors(t *testing.T) {
	failure := errors.New("synthetic iterator error")
	upstream := &countedIterator{err: failure}
	iter := &outputIterator[int]{source: upstream, context: context.Background(), transform: transformers.Identity, remaining: 2}
	require.False(t, iter.Next())
	require.ErrorIs(t, iter.Err(), failure)
	require.ErrorIs(t, ShowJSONIterator[int](upstream, 0, ShowJSONOpts{Format: "explore"}), failure)
}

func TestZeroIteratorLimitDoesNotTouchOutput(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	require.NoError(t, output.Close())
	for _, format := range []string{"json", "explore"} {
		t.Run(format, func(t *testing.T) {
			source := &countedIterator{}
			require.NoError(t, ShowJSONIterator[int](source, 0, ShowJSONOpts{Format: format, Stdout: output}))
			require.Zero(t, source.reads)
		})
	}
}
