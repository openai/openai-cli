package app

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"testing"

	"github.com/openai/openai-cli/internal/jsonview"
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

type localFilesWithLookup struct {
	*sliceIterator[openai.FileObject]
	lookup func() error
	err    error
}

func (it *localFilesWithLookup) Next() bool {
	if it.index == 1 {
		it.err = it.lookup()
	}
	return it.err == nil && it.sliceIterator.Next()
}

func (it *localFilesWithLookup) Err() error { return it.err }

func TestRegisteredLocalListAfterRawLookup(t *testing.T) {
	const pageSize = 3
	const rawPage = `{"data":[{"id":"file_1"}],"has_more":true}`
	files := make([]openai.FileObject, 10)
	for i := range files {
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"id":"file_%d"}`, i+1)), &files[i]))
	}
	for _, tc := range []struct {
		name    string
		limit   int64
		screens int
		cancel  bool
		during  string
	}{
		{"quit", -1, 1, false, ""}, {"advance then quit", -1, 2, false, ""},
		{"cancel", -1, 1, true, ""}, {"exact cap", 2 * pageSize, 2, false, ""},
		{"raw lookup during Next", -1, 2, false, "raw"},
		{"sibling continuation during Next", -1, 2, false, "sibling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			respond := func(req *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				calls++
				if tc.during == "sibling" && req.Method == http.MethodGet && req.URL.RequestURI() == "/v1/files?after=file_1" {
					return listResponse(req, `{"data":[{"id":"sibling_2"}],"has_more":false}`), nil
				}
				if req.Method != http.MethodGet || req.URL.RequestURI() != "/v1/files" {
					return nil, fmt.Errorf("unexpected lookup: %s %s", req.Method, req.URL)
				}
				return listResponse(req, rawPage), nil
			}
			client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
				option.WithBaseURL("http://127.0.0.1:1/v1/"), option.WithMiddleware(registeredListRequest, respond))
			rows := &sliceIterator[openai.FileObject]{items: files}
			list := &registeredList{pageSize: pageSize}
			var raw []byte
			next, quit := iter.Pull2(list.pages(ctx, func(ctx context.Context) error {
				lookup := func() error { return client.Get(ctx, "files", nil, &raw) }
				var source jsonview.Iterator[openai.FileObject] = rows
				switch tc.during {
				case "sibling":
					sibling := PrepareListIterator(ctx, func(ctx context.Context) *pagination.CursorPageAutoPager[openai.FileObject] {
						return client.Files.ListAutoPaging(ctx, openai.FileListParams{})
					})
					if !sibling.Next() {
						return fmt.Errorf("missing first sibling row")
					}
					source = &localFilesWithLookup{sliceIterator: rows, lookup: func() error {
						if !sibling.Next() || sibling.Current().ID != "sibling_2" {
							return fmt.Errorf("missing second sibling row")
						}
						return sibling.Err()
					}}
				case "raw":
					source = &localFilesWithLookup{sliceIterator: rows, lookup: lookup}
				default:
					if err := lookup(); err != nil {
						return err
					}
				}
				// Forward the same SDK item type that a Files autopager would return.
				local := struct {
					jsonview.Iterator[openai.FileObject]
				}{source}
				return ShowJSONIterator(local, tc.limit, ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem})
			}))
			defer quit()
			for screen := 1; screen <= tc.screens; screen++ {
				page, err, ok := next()
				require.True(t, ok)
				require.NoError(t, err)
				shown := screen * pageSize
				require.LessOrEqual(t, rows.index, shown+1, "local source read past the next row")
				require.Len(t, page.Items, pageSize)
				for i, item := range page.Items {
					require.Equal(t, files[shown-pageSize+i].ID, item.Get("id").Str)
				}
				require.Equal(t, tc.limit < 0 || int64(shown) < tc.limit, page.More)
			}
			reads := rows.index
			if tc.cancel {
				cancel()
				_, err, ok := next()
				require.True(t, ok)
				require.ErrorIs(t, err, context.Canceled)
			}
			quit()
			require.Equal(t, reads, rows.index)
			if tc.limit > 0 {
				require.EqualValues(t, tc.limit, reads)
			}
			if tc.during == "sibling" {
				require.Equal(t, 2, calls)
			} else {
				require.Equal(t, 1, calls)
				require.Equal(t, rawPage, string(raw))
			}
		})
	}
}

type preparedModelIDs struct {
	PreparedListIterator[openai.Model]
}

func (models preparedModelIDs) Current() string {
	return models.PreparedListIterator.Current().ID
}

func TestPreparedSingleResponseKeepsAllLoadedRows(t *testing.T) {
	for _, mode := range []string{"direct", "projected"} {
		t.Run(mode, func(t *testing.T) {
			checkPreparedSingleResponse(t, mode == "projected")
		})
	}
}

func checkPreparedSingleResponse(t *testing.T, projected bool) {
	t.Helper()
	calls := 0
	respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		calls++
		return listResponse(r, `{"data":[{"id":"one"},{"id":"two"},{"id":"three"}]}`), nil
	}
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0), option.WithMiddleware(registeredListRequest, respond))
	list := &registeredList{pageSize: 2}
	next, quit := iter.Pull2(list.pages(t.Context(), func(ctx context.Context) error {
		models := PrepareListIterator(ctx, func(ctx context.Context) *pagination.PageAutoPager[openai.Model] {
			return client.Models.ListAutoPaging(ctx)
		})
		opts := ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem}
		if projected {
			return ShowJSONIterator(preparedModelIDs{models}, -1, opts)
		}
		return ShowJSONIterator(models, -1, opts)
	}))
	defer quit()
	page, err, ok := next()
	require.True(t, ok)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	if projected {
		require.Equal(t, "three", page.Items[2].String())
	} else {
		require.Equal(t, "three", page.Items[2].Get("id").Str)
	}
	require.False(t, page.More)
	require.Equal(t, 1, calls)
}
