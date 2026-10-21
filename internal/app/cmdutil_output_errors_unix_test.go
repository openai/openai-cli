//go:build !windows

package app

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShowJSONIteratorClosedOutput(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, r.Close())
	defer w.Close()
	previous := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = previous })
	for _, format := range []string{"auto", "text", "json", "raw"} {
		t.Run(format, func(t *testing.T) {
			iter := &sliceIterator[string]{items: []string{strings.Repeat("x", 4000)}}
			require.NoError(t, ShowJSONIterator[string](iter, -1, ShowJSONOpts{Format: format, Stdout: w}))
		})
	}
}

func TestReadableIteratorClosedInjectedOutputPreservesFailure(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, r.Close())
	defer w.Close()
	// This writer is not process stdout. Its failure must reach its caller.
	for _, format := range []string{"auto", "text"} {
		iter := &sliceIterator[string]{items: []string{strings.Repeat("x", 4000)}}
		require.ErrorIs(t, ShowJSONIterator[string](iter, -1, ShowJSONOpts{Format: format, Stdout: w}), syscall.EPIPE)
	}
}

func TestReadableProcessStdoutPreservesUpstreamPipeFailure(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	t.Cleanup(func() { os.Stdout = previous })
	for _, format := range []string{"auto", "text"} {
		upstream := fmt.Errorf("synthetic upstream: %w", syscall.EPIPE)
		iter := &failingOutputIterator{err: upstream}
		require.ErrorIs(t, ShowJSONIterator[string](iter, -1, ShowJSONOpts{Format: format, Stdout: output}), upstream)
	}
}
