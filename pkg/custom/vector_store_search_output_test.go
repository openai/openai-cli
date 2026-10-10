package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVectorStoreSearchEmptyOutputFailures(t *testing.T) {
	opts := ShowJSONOpts{
		Context: t.Context(), Format: "text", OutputKind: OutputPageItem,
		Operation: "(resource) vector_stores > (method) search",
	}
	t.Run("upstream", func(t *testing.T) {
		var out bytes.Buffer
		opts.Stdout = &out
		failure := errors.New("search failed")
		err := ShowJSONIterator(&transformTestIterator{err: failure}, -1, opts)
		require.ErrorIs(t, err, failure)
		require.Empty(t, out.String())
	})
	t.Run("canceled", func(t *testing.T) {
		var out bytes.Buffer
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		opts.Context, opts.Stdout = ctx, &out
		require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{}, -1, opts), context.Canceled)
		require.Empty(t, out.String())
		opts.Context = t.Context()
	})
	t.Run("write error", func(t *testing.T) {
		failure := errors.New("output failed")
		opts.Stdout = failOutputWriter{failure}
		require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{}, -1, opts), failure)
	})
	t.Run("short write", func(t *testing.T) {
		opts.Stdout = failOutputWriter{}
		require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{}, -1, opts), io.ErrShortWrite)
	})
}
