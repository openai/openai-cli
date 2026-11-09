package app

import (
	"context"
	"iter"
	"net/http"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
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

func TestRegisteredLocalListAfterUnusedSDKPager(t *testing.T) {
	const pageSize = 3
	calls := 0
	respond := func(req *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		calls++
		return listResponse(req, `{"data":[],"has_more":true}`), nil
	}
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
		option.WithMiddleware(registeredListRequest, respond))
	rows := &sliceIterator[int]{items: make([]int, 12)}
	list := &registeredList{pageSize: pageSize}
	next, quit := iter.Pull2(list.pages(t.Context(), func(ctx context.Context) error {
		PrepareListIterator(ctx, func(sourceCtx context.Context) *pagination.CursorPageAutoPager[openai.FileObject] {
			return client.Files.ListAutoPaging(sourceCtx, openai.FileListParams{})
		})
		return ShowJSONIterator(rows, -1, ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem})
	}))
	defer quit()

	page, err, ok := next()
	require.True(t, ok)
	require.NoError(t, err)
	require.Len(t, page.Items, pageSize)
	require.True(t, page.More)
	quit()
	require.Equal(t, pageSize+1, rows.index)
	require.Equal(t, 1, calls)
}
