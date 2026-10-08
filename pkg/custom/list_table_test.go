package custom

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRenderListTablePage(t *testing.T) {
	items := []gjson.Result{gjson.Parse(`{"object":"batch","id":"batch_synthetic","status":"completed","created_at":1}`)}
	original := items[0].Raw
	content, supported, err := renderListTablePage("(resource) batches > (method) list", items, 80)
	require.NoError(t, err)
	require.True(t, supported)
	require.Contains(t, content, "ID")
	require.Contains(t, content, "STATUS")
	require.Contains(t, content, "batch_synthetic")
	require.Equal(t, 1, strings.Count(content, resourceSummaryHint))
	require.Equal(t, original, items[0].Raw)
	// Rerendering reads only loaded values and cannot mutate their original JSON.
	_, supported, err = renderListTablePage("(resource) batches > (method) list", items, 1)
	require.NoError(t, err)
	require.False(t, supported)
	require.Equal(t, original, items[0].Raw)
}

func TestRenderListTablePageEmptyAndUnsupported(t *testing.T) {
	content, supported, err := renderListTablePage("(resource) files > (method) list", nil, 80)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, "No results.\n", content)
	for _, op := range []string{"", "(resource) files > (method) retrieve", "(resource) images > (method) models", "(resource) models > (method) list"} {
		content, supported, err = renderListTablePage(op, nil, 80)
		require.NoError(t, err)
		require.False(t, supported)
		require.Empty(t, content)
	}
}
