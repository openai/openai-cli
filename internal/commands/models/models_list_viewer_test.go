package models

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/openai/openai-cli/internal/output/listview"
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
	pages, err := localModelPages(ctx, items)
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

func TestLocalModelPagesValidateTheWholeCollection(t *testing.T) {
	valid := gjson.Parse(`{"object":"model","id":"model-v","owned_by":"synthetic"}`)
	items := slices.Repeat([]gjson.Result{valid}, modelListPageSize)
	items = append(items, gjson.Parse(`{"object":"model","id":"model-a","id":"ambiguous"}`))
	pages, err := localModelPages(t.Context(), items)
	require.NoError(t, err)
	page, err := pages.next()
	require.NoError(t, err)
	require.False(t, page.More)
	require.Equal(t, items, page.Items)
}

func TestLocalModelPagesStopOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	items := slices.Repeat([]gjson.Result{gjson.Parse(`{"object":"model","id":"model-v"}`)}, modelListPageSize+1)
	pages, err := localModelPages(ctx, items)
	require.NoError(t, err)
	first, err := pages.next()
	require.NoError(t, err)
	require.True(t, first.More)
	cancel()
	_, err = pages.next()
	require.ErrorIs(t, err, context.Canceled)
	_, err = localModelPages(ctx, items)
	require.ErrorIs(t, err, context.Canceled)
}
