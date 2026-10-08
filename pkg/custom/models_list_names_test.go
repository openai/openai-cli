package custom

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestModelsListNamesIgnoreMetadata(t *testing.T) {
	opts := ShowJSONOpts{Context: context.Background(), Operation: "(resource) models > (method) list", OutputKind: OutputPageItem}
	items := []gjson.Result{
		gjson.Parse(`{"object":"model","id":"model-z","owned_by":"synthetic-owner","shutdown_date":"2030-01-01","new_metadata":{"message":"synthetic detail"}}`),
		gjson.Parse(`{"object":"model","id":"model-a","created":123,"owned_by":"synthetic"}`),
		gjson.Parse(`{"object":"model","id":"model-a"}`),
	}
	content, err := renderListNavigationPage(opts, items, 80)
	require.NoError(t, err)
	require.Equal(t, "ID\nmodel-a\nmodel-a\nmodel-z\n\nListed 3 models.\nDetails: --format json\n", content)
	// p remains an explicit request for complete readable records.
	labels, err := renderListNavigationLabels(opts, items)
	require.NoError(t, err)
	require.Contains(t, labels, "ID: model-z")
	require.Contains(t, labels, "synthetic detail")
	require.Contains(t, labels, "2030-01-01")
	// Explicit text never selects the automatic names-only presentation.
	opts.Format = "text"
	text, err := renderListNavigationPage(opts, items, 80)
	require.NoError(t, err)
	require.Equal(t, labels, text)
}

func TestModelsListNamesPreserveAndEscapeIDs(t *testing.T) {
	opts := ShowJSONOpts{Context: context.Background()}
	name := "model-" + strings.Repeat("long", 80)
	items := []gjson.Result{
		{Type: gjson.JSON, Raw: `{"object":"model","id":"` + name + `"}`},
		gjson.Parse(`{"object":"model","id":"model-\u001b[31m\n\t\u202e"}`),
	}
	content, supported, err := renderModelsListNames(opts, items, 20)
	require.NoError(t, err)
	require.True(t, supported)
	require.Contains(t, content, name+"\n")
	require.Contains(t, content, `model-\u001b[31m\n\t\u202e`)
	require.NotContains(t, content, "\x1b")
	require.NotContains(t, content, "\u202e")
}

func TestModelsListNamesEmptyAndCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	opts := ShowJSONOpts{Context: ctx}
	content, supported, err := renderModelsListNames(opts, nil, 80)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, "No results.\n", content)
	cancel()
	_, _, err = renderModelsListNames(opts, nil, 80)
	require.ErrorIs(t, err, context.Canceled)
}

func TestModelsListNamesKeepExplicitDescendingOrder(t *testing.T) {
	options, err := parseModelsListOptions("", false, "~id")
	require.NoError(t, err)
	opts := ShowJSONOpts{Context: context.WithValue(t.Context(), modelsListOptionsKey{}, options)}
	items := []gjson.Result{
		gjson.Parse(`{"object":"model","id":"model-a"}`),
		gjson.Parse(`{"object":"model","id":"model-z"}`),
		gjson.Parse(`{"object":"model","id":"model-a"}`),
	}
	for _, width := range []int{80, 20, 110} {
		content, supported, err := renderModelsListNames(opts, items, width)
		require.NoError(t, err)
		require.True(t, supported)
		require.Contains(t, content, "ID\nmodel-z\nmodel-a\nmodel-a\n")
	}
}
