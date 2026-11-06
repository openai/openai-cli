package app

import (
	"context"
	"errors"
	"os"
	"testing"

	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
)

type countedIterator struct {
	reads int
	err   error
}

func (i *countedIterator) Next() bool   { i.reads++; return i.reads <= 5 && i.err == nil }
func (i *countedIterator) Current() int { return i.reads }
func (i *countedIterator) Err() error   { return i.err }

func TestIteratorLimitsPreserveErrors(t *testing.T) {
	failure := errors.New("synthetic iterator error")
	upstream := &countedIterator{err: failure}
	iter := clioutput.NewIterator(upstream, clioutput.IteratorOptions{
		Context:   context.Background(),
		Transform: transformers.Identity,
		Limit:     2,
	})
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
