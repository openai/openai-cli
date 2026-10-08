package custom

import (
	"context"
	"strings"
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
	// p remains an explicit request for complete readable records.
	labels, err := renderListNavigationLabels(opts, items)
	require.NoError(t, err)
	require.Contains(t, labels, "ID: model-z")
	require.Contains(t, labels, "synthetic detail")
	require.Contains(t, labels, "2030-01-01")
	// Explicit text retains the existing complete readable presentation.
	opts.Format = "text"
	text, err := renderListNavigationPage(opts, items, 80)
	require.NoError(t, err)
	require.Equal(t, labels, text)
}

func TestModelsListTableFallbackPreservesAndEscapesBothFields(t *testing.T) {
	opts := ShowJSONOpts{Context: context.Background()}
	name := "model-" + strings.Repeat("long", 80)
	items := []gjson.Result{
		{Type: gjson.JSON, Raw: `{"object":"model","id":"` + name + `","owned_by":"long synthetic owner","unrelated":"not displayed"}`},
		gjson.Parse(`{"object":"model","id":"model-\u001b[31m\n\t\u202e","owned_by":"owner-\u001b[31m\n\t\u202e"}`),
	}
	content, supported, err := renderModelsListTable(opts, items, 20)
	require.NoError(t, err)
	require.True(t, supported)
	require.Contains(t, content, "ID: "+name+"\nOwned by: long synthetic owner\n")
	require.Contains(t, content, `model-\u001b[31m\n\t\u202e`)
	require.Contains(t, content, `Owned by: owner-\u001b[31m\n\t\u202e`)
	require.NotContains(t, content, "not displayed")
	require.NotContains(t, content, "\x1b")
	require.NotContains(t, content, "\u202e")
}

func TestModelsListTableEmptyAndCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	opts := ShowJSONOpts{Context: ctx}
	content, supported, err := renderModelsListTable(opts, nil, 80)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, "No results.\n", content)
	cancel()
	_, _, err = renderModelsListTable(opts, nil, 80)
	require.ErrorIs(t, err, context.Canceled)
}

func TestModelsListTableKeepsOwnerAssociationAndDescendingOrder(t *testing.T) {
	options, err := parseModelsListOptions("", false, "~id")
	require.NoError(t, err)
	opts := ShowJSONOpts{Context: context.WithValue(t.Context(), modelsListOptionsKey{}, options)}
	items := []gjson.Result{
		gjson.Parse(`{"object":"model","id":"model-a","owned_by":"first-a-owner"}`),
		gjson.Parse(`{"object":"model","id":"model-z","owned_by":"z-owner"}`),
		gjson.Parse(`{"object":"model","id":"model-a","owned_by":"second-a-owner"}`),
	}
	for _, width := range []int{80, 20, 110} {
		content, supported, err := renderModelsListTable(opts, items, width)
		require.NoError(t, err)
		require.True(t, supported)
		if width == 20 {
			require.Contains(t, content, "ID: model-z\nOwned by: z-owner\n\nID: model-a\nOwned by: first-a-owner\n\nID: model-a\nOwned by: second-a-owner\n")
		} else {
			require.Contains(t, content, "ID       OWNER\nmodel-z  z-owner\nmodel-a  first-a-owner\nmodel-a  second-a-owner\n")
		}
	}
}

func TestModelsListTableUnknownOwnersRemainCompact(t *testing.T) {
	opts := ShowJSONOpts{Context: t.Context()}
	for _, fields := range []string{
		``, `,"owned_by":null`, `,"owned_by":""`, `,"owned_by":123`,
		`,"owned_by":false`, `,"owned_by":[]`, `,"owned_by":{"name":"not-an-owner-string"}`,
		`,"owned_by":"first-owner","owned_by":"second-owner"`,
	} {
		items := []gjson.Result{gjson.Parse(`{"object":"model","id":"model-a","metadata":"omit this"` + fields + `}`)}
		for _, width := range []int{80, 16} {
			content, supported, err := renderModelsListTable(opts, items, width)
			require.NoError(t, err)
			require.True(t, supported)
			if width == 80 {
				require.Contains(t, content, "ID       OWNER\nmodel-a  (unknown)\n")
			} else {
				require.Contains(t, content, "ID: model-a\nOwned by: (unknown)\n")
			}
			require.NotContains(t, content, "omit this")
			require.NotContains(t, content, "first-owner")
			require.NotContains(t, content, "second-owner")
			require.Contains(t, content, "Listed 1 model.")
		}
	}
}

func TestModelsListTableUsesLabelsBeforeShorteningOwners(t *testing.T) {
	opts := ShowJSONOpts{Context: t.Context()}
	owner := "synthetic-owner-" + strings.Repeat("x", 50)
	items := []gjson.Result{gjson.Parse(`{"object":"model","id":"model-a","owned_by":"` + owner + `","unrelated":true}`)}
	for _, width := range []int{10, 80, 160} {
		content, supported, err := renderModelsListTable(opts, items, width)
		require.NoError(t, err)
		require.True(t, supported)
		require.Contains(t, content, "ID: model-a\nOwned by: "+owner+"\n")
		require.NotContains(t, content, "…")
		require.NotContains(t, content, "Unrelated:")
	}
}

func TestModelsListTableEscapesBothCells(t *testing.T) {
	opts := ShowJSONOpts{Context: t.Context()}
	items := []gjson.Result{gjson.Parse(`{"object":"model","id":"model\t1","owned_by":"owner\n2\u202e"}`)}
	content, supported, err := renderModelsListTable(opts, items, 80)
	require.NoError(t, err)
	require.True(t, supported)
	require.Contains(t, content, `model\t1  owner\n2\u202e`)
	require.NotContains(t, content, "\t")
	require.NotContains(t, content, "\u202e")
	require.Len(t, strings.Split(strings.TrimSuffix(content, "\n"), "\n"), 5)
}
