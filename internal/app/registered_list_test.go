package app

import (
	"context"
	"iter"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisteredLocalListStopsReadingOnQuit(t *testing.T) {
	rows := &sliceIterator[int]{items: make([]int, localListPageSize*10)}
	next, quit := iter.Pull2(new(registeredList).pages(t.Context(), func(ctx context.Context) error {
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

func TestRegisteredLocalListResizesBetweenPages(t *testing.T) {
	rows := &sliceIterator[int]{items: make([]int, 12)}
	list := &registeredList{pageSize: 3}
	next, quit := iter.Pull2(list.pages(t.Context(), func(ctx context.Context) error {
		return ShowJSONIterator(rows, -1, ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem})
	}))
	defer quit()
	for _, size := range []int{3, 7, 2} {
		list.pageSize = size
		page, err, ok := next()
		require.True(t, ok)
		require.NoError(t, err)
		require.Len(t, page.Items, size)
		require.Equal(t, size != 2, page.More)
	}
	require.False(t, list.unknownTotal)
}
