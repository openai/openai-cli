package models

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestLocalModelPagesPreserveGlobalOrderAndDuplicateRecords(t *testing.T) {
	// Equal IDs start together but cross a page boundary after global sorting.
	items := []gjson.Result{
		gjson.Parse(`{"object":"model","id":"model-001","owned_by":"first"}`),
		gjson.Parse(`{"object":"model","id":"model-001","owned_by":"second"}`),
	}
	for i := 2; i <= modelListPageSize; i++ {
		items = append(items, gjson.Parse(fmt.Sprintf(`{"object":"model","id":"model-%03d"}`, i)))
	}
	original := slices.Clone(items)
	options, err := parseModelsListOptions("", false, "~id")
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), modelsListOptionsKey{}, options)
	pages, err := localModelPages(ctx, items, false, false)
	require.NoError(t, err)
	first, err := pages.next()
	require.NoError(t, err)
	require.True(t, first.More)
	require.Len(t, first.Items, modelListPageSize)
	require.Equal(t, "model-100", first.Items[0].Get("id").Str)
	require.Equal(t, "first", first.Items[modelListPageSize-1].Get("owned_by").Str)
	final, err := pages.next()
	require.NoError(t, err)
	require.False(t, final.More)
	require.Len(t, final.Items, 1)
	require.Equal(t, "second", final.Items[0].Get("owned_by").Str)
	content, supported, err := renderTable(ctx, final.Items, 80, true, listview.Style{}, len(items))
	require.NoError(t, err)
	require.True(t, supported)
	require.Contains(t, content, "Showing 1 of 101 models.\nDetails: --format json\n")
	require.Equal(t, original, items)
}

func TestCappedModelPagesUseOnlyKnownMatchCounts(t *testing.T) {
	var records []clioutput.JSON
	for i := 20; i > 0; i-- {
		records = append(records, clioutput.JSON{Result: gjson.Parse(fmt.Sprintf(`{"object":"model","id":"model-%03d"}`, i))})
	}
	for _, test := range []struct {
		name, filter string
		limit        int64
		rows, total  int
		firstID      string
	}{
		{"source cap reached", "", 3, 3, 0, "model-018"},
		{"source exhausted", "", 25, 20, 20, "model-001"},
		{"selection capped", "id~model-00[1-5]", 2, 2, 5, "model-001"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			page := pagination.Page[clioutput.JSON]{Data: records}
			var source jsonview.Iterator[clioutput.JSON] = pagination.NewPageAutoPager(&page, nil)
			limit := test.limit
			if test.filter != "" {
				options, err := parseModelsListOptions(test.filter, true, "id")
				require.NoError(t, err)
				ctx = context.WithValue(ctx, modelsListOptionsKey{}, options)
				var handled bool
				ctx, source, handled, err = Select(ctx, source, limit)
				require.NoError(t, err)
				require.True(t, handled)
				limit = -1 // Selection already applied the cap.
			}
			iter := clioutput.NewIterator(source, clioutput.IteratorOptions{Context: ctx, Transform: transform.Identity, Limit: limit})
			var items []gjson.Result
			for iter.Next() {
				items = append(items, iter.Current().Result)
			}
			require.NoError(t, iter.Err())
			pages, err := localModelPages(ctx, items, true, iter.LimitReached())
			require.NoError(t, err)
			got, err := pages.next()
			require.NoError(t, err)
			require.Len(t, got.Items, test.rows)
			require.Equal(t, test.firstID, got.Items[0].Get("id").Str)
			require.Equal(t, test.total, got.Total)
		})
	}
}

func TestLocalModelPagesValidateTheWholeCollection(t *testing.T) {
	valid := gjson.Parse(`{"object":"model","id":"model-v","owned_by":"synthetic"}`)
	items := slices.Repeat([]gjson.Result{valid}, modelListPageSize)
	items = append(items, gjson.Parse(`{"object":"model","id":"model-a","id":"ambiguous"}`))
	pages, err := localModelPages(t.Context(), items, false, false)
	require.NoError(t, err)
	page, err := pages.next()
	require.NoError(t, err)
	require.False(t, page.More)
	require.Equal(t, items, page.Items)
}

func TestLocalModelPagesStopOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	items := slices.Repeat([]gjson.Result{gjson.Parse(`{"object":"model","id":"model-v"}`)}, modelListPageSize+1)
	pages, err := localModelPages(ctx, items, false, false)
	require.NoError(t, err)
	first, err := pages.next()
	require.NoError(t, err)
	require.True(t, first.More)
	cancel()
	_, err = pages.next()
	require.ErrorIs(t, err, context.Canceled)
	_, err = localModelPages(ctx, items, false, false)
	require.ErrorIs(t, err, context.Canceled)
}
