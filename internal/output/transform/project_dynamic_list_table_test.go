package transform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDynamicListTableProjectsUnknownFields(t *testing.T) {
	first := gjson.Parse(`{"new_count":9007199254740993,"id":"x","name":"Example","v2_option":true,"optional":null}`)
	second := gjson.Parse(`{"optional":null,"v2_option":false,"name":"Next","new_count":1e-2,"id":"y"}`)
	for _, operation := range []string{"(resource) future > (method) list", "(resource) future.items > (method) list_items"} {
		result, err := ProjectListTable(t.Context(), operation, []gjson.Result{first, second})
		require.NoError(t, err)
		require.True(t, result.FullValues)
		require.Equal(t, []string{"ID", "NAME", "NEW COUNT", "OPTIONAL", "V2 OPTION"}, result.Headers)
		require.Equal(t, [][]string{{"x", "Example", "9007199254740993", "null", "true"}, {"y", "Next", "1e-2", "null", "false"}}, result.Rows)
	}
}

func TestDynamicListTableAcceptsScalarCatalog(t *testing.T) {
	items := []gjson.Result{gjson.Parse(`"one"`), gjson.Parse(`"two"`)}
	result, err := ProjectListTable(t.Context(), "(resource) catalog > (method) list", items)
	require.NoError(t, err)
	require.Equal(t, []string{"VALUE"}, result.Headers)
	require.Equal(t, [][]string{{"one"}, {"two"}}, result.Rows)

	mixed := []gjson.Result{gjson.Parse(`null`), gjson.Parse(`{"value":null}`)}
	result, err = ProjectListTable(t.Context(), "(resource) catalog > (method) list", mixed)
	require.NoError(t, err)
	require.Empty(t, result.Headers, "Mixing scalar and object records would make their shapes indistinguishable.")
}

func TestDynamicListTableRequiresCompleteFields(t *testing.T) {
	first := gjson.Parse(`{"id":"a","name":"First"}`)
	for _, item := range []string{
		`{}`, `{"id":"b"}`, `{"id":"b","extra":42}`, `{"id":"b","name":"Second","unknown":null}`,
		`{"id":"b","name":"Second","name":"duplicate"}`, `{"foo_bar":"first","foo bar":"second"}`,
		`{"id":"b","details":{"nested":"retain this"}}`, `{"id":"b","last_error":"Read me"}`,
		`{"id":"b","signing_secret":"synthetic"}`, `{"id":"b","name":"bad"`,
	} {
		result, err := ProjectListTable(t.Context(), "(resource) future > (method) list", []gjson.Result{first, gjson.Parse(item)})
		require.NoError(t, err, item)
		require.Empty(t, result.Headers, item)
		require.Empty(t, result.Rows, item)
	}
	for _, item := range []string{
		`{"foo_bar":"first","foo bar":"second"}`, `{" ":"empty label"}`,
		`{"id":"b","details":{"nested":"retain this"}}`, `{"id":"b","last_error":"Read me"}`,
		`{"id":"b","signing_secret":"synthetic"}`, `{"name":"first","name":"duplicate"}`,
	} {
		result, err := ProjectListTable(t.Context(), "(resource) future > (method) list", []gjson.Result{gjson.Parse(item)})
		require.NoError(t, err)
		require.Empty(t, result.Headers, item)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := ProjectListTable(ctx, "(resource) future > (method) list", []gjson.Result{first})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result.Headers)
}

func TestListMethodBoundary(t *testing.T) {
	for _, valid := range []string{"(resource) files > (method) list", "(resource) future.items > (method) list_items"} {
		require.True(t, IsResourceListOperation(valid))
	}
	for _, invalid := range []string{"", "future list", "(resource)  > (method) list", "(resource) files > (method) list_",
		"(resource) files > (method) retrieve", "(resource) files > (method) list_items > extra", "(resource) files > (method) shortlist"} {
		require.False(t, IsResourceListOperation(invalid))
	}
}

func TestDynamicListTableRetainsNestedFailures(t *testing.T) {
	vector := `{"id":"vs_example","object":"vector_store","name":"Guide","status":"completed","file_counts":`
	api := "(resource) vector_stores > (method) list"
	for _, tc := range []struct {
		counts string
		fits   bool
	}{
		{`{"in_progress":0,"completed":2,"failed":0,"cancelled":0,"total":2}`, true},
		{`{"in_progress":0,"completed":1,"failed":1,"cancelled":0,"total":2}`, false},
		{`{"in_progress":0,"completed":1,"failed":0,"cancelled":0,"total":2}`, false},
		{`{"in_progress":0,"completed":2,"failed":0,"cancelled":0,"total":2,"hold":1}`, false},
	} {
		table, err := ProjectListTable(t.Context(), api, []gjson.Result{gjson.Parse(vector + tc.counts + "}")})
		require.NoError(t, err)
		if tc.fits {
			require.Equal(t, []string{"ID", "NAME", "STATUS", "FILES"}, table.Headers)
			require.Equal(t, [][]string{{"vs_example", "Guide", "completed", "2"}}, table.Rows)
		} else {
			require.Empty(t, table.Headers)
		}
	}
}

func TestHandwrittenRecordTableWithoutOperationMetadata(t *testing.T) {
	item := gjson.Parse(`{"name":"offline snapshot","available":true}`)
	table, err := ProjectRecordTable(t.Context(), "", []gjson.Result{item})
	require.NoError(t, err)
	require.Equal(t, []string{"NAME", "AVAILABLE"}, table.Headers)
	require.Equal(t, [][]string{{"offline snapshot", "true"}}, table.Rows)
	ordinary, err := ProjectListTable(t.Context(), "", []gjson.Result{item})
	require.NoError(t, err)
	require.Empty(t, ordinary.Headers)
}
