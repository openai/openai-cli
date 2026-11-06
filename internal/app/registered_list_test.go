package app

import (
	"context"
	"iter"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisteredLocalListStopsReadingOnQuit(t *testing.T) {
	rows := &sliceIterator[int]{items: make([]int, localListPageSize*10)}
	next, quit := iter.Pull2(registeredListPages(t.Context(), func(ctx context.Context) error {
		return ShowJSONIterator(rows, -1, ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem})
	}))
	defer quit()
	page, err, ok := next()
	require.True(t, ok)
	require.NoError(t, err)
	require.Len(t, page.Items, localListPageSize)
	require.True(t, page.More)
	quit()
	require.Equal(t, localListPageSize+1, rows.index)
}
