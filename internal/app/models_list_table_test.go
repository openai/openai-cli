package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestModelsListTableOmitsOtherMetadata(t *testing.T) {
	opts := ShowJSONOpts{Context: context.Background(), Operation: "(resource) models > (method) list", OutputKind: OutputPageItem}
	items := []gjson.Result{
		gjson.Parse(`{"object":"model","id":"model-z","owned_by":"synthetic-owner","shutdown_date":"2030-01-01","new_metadata":{"message":"synthetic detail"}}`),
		gjson.Parse(`{"object":"model","id":"model-a","created":123,"owned_by":"synthetic"}`),
		gjson.Parse(`{"object":"model","id":"model-a"}`),
	}
	content, err := renderListNavigationPage(opts, items, 80)
	require.NoError(t, err)
	require.Equal(t, "ID       OWNER\nmodel-a  synthetic\nmodel-a  (unknown)\nmodel-z  synthetic-owner\n\nListed 3 models.\nDetails: --format json\n", content)
	require.NotContains(t, content, "2030-01-01")
	require.NotContains(t, content, "synthetic detail")
	// The readable fallback preserves unfamiliar metadata.
	labels, err := renderListNavigationLabels(opts, items)
	require.NoError(t, err)
	require.Contains(t, labels, "ID: model-z")
	require.Contains(t, labels, "synthetic detail")
	require.Contains(t, labels, "2030-01-01")
	// Explicit text retains the existing readable presentation.
	opts.Format = "text"
	text, err := renderListNavigationPage(opts, items, 80)
	require.NoError(t, err)
	require.Equal(t, labels, text)
}
