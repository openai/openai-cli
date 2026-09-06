package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingOutputIterator struct {
	sliceIterator[string]
	err error
}

func (it *failingOutputIterator) Err() error { return it.err }

func TestShowJSONIteratorPreservesUpstreamErrors(t *testing.T) {
	for _, upstream := range []error{
		errors.New("upstream disconnected"),
		errors.New("upstream broken pipe"),
		fmt.Errorf("upstream transport: %w", syscall.EPIPE),
	} {
		t.Run(upstream.Error(), func(t *testing.T) {
			output, err := os.CreateTemp(t.TempDir(), "output")
			require.NoError(t, err)
			defer output.Close()
			previous := os.Stdout
			os.Stdout = output
			t.Cleanup(func() { os.Stdout = previous })
			iter := &failingOutputIterator{
				sliceIterator: sliceIterator[string]{items: []string{strings.Repeat("x", 4000), "last item"}},
				err:           upstream,
			}
			err = ShowJSONIterator[string](iter, -1, ShowJSONOpts{Format: "raw", Stdout: output})
			require.ErrorIs(t, err, upstream)
			contents, err := os.ReadFile(output.Name())
			require.NoError(t, err)
			require.Contains(t, string(contents), "last item")
		})
	}
}

type failingOutputMarshaler struct{ err error }

func (value failingOutputMarshaler) MarshalJSON() ([]byte, error) { return nil, value.err }

func TestShowJSONIteratorPreservesFormatterError(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	t.Cleanup(func() { os.Stdout = previous })
	upstream := errors.New("marshal broken pipe")
	iter := &sliceIterator[any]{items: []any{strings.Repeat("x", 4000), failingOutputMarshaler{upstream}}}
	require.ErrorIs(t, ShowJSONIterator[any](iter, -1, ShowJSONOpts{Format: "raw", Stdout: output}), upstream)
}
