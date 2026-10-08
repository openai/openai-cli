package transformers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectListTableCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, operation := range []string{
		"(resource) files > (method) list",
		"(resource) batches > (method) list",
		"(resource) admin.organization.projects > (method) list",
		"unsupported",
	} {
		headers, rows, supported, err := ProjectListTable(ctx, operation, nil)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, supported)
		require.Nil(t, headers)
		require.Nil(t, rows)
	}
}

func TestListTableCountsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancel inside the existing summary's field scan, after entry checks.
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 3}
	counts := gjson.Parse(`{"total":1,"completed":1,"failed":0}`)
	supported, err := listTableCountsWithoutFailures(controlled, counts)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, supported, "Cancellation must not become ordinary unsupported-count fallback.")
}

func TestProjectListTableKnownResources(t *testing.T) {
	for _, test := range []struct {
		resource, input string
		headers, row    []string
	}{
		{
			"files",
			`{"id":"file_exact:identifier-001","object":"file","filename":"training data.jsonl","purpose":"fine-tune","bytes":1536,"status":"processed","status_details":null,"created_at":123,"expires_at":456}`,
			[]string{"ID", "FILENAME", "PURPOSE", "SIZE", "STATUS"},
			[]string{"file_exact:identifier-001", "training data.jsonl", "fine-tune", "1.5 KiB", "processed"},
		},
		{
			"batches",
			`{"id":"batch_example","object":"batch","status":"completed","completion_window":"24h","endpoint":"/v1/responses","input_file_id":"file_input","output_file_id":"file_output","error_file_id":null,"errors":null,"created_at":123,"request_counts":{"total":9007199254740993,"completed":9007199254740993,"failed":0},"usage":{"input_tokens":123},"metadata":{"label":"synthetic"}}`,
			[]string{"ID", "STATUS"},
			[]string{"batch_example", "completed"},
		},
		{
			"admin.organization.projects",
			`{"id":"proj_example","object":"organization.project","name":"Synthetic project","status":"active","created_at":123,"archived_at":null,"external_key_id":null,"residency":"GLOBAL"}`,
			[]string{"ID", "NAME", "STATUS"},
			[]string{"proj_example", "Synthetic project", "active"},
		},
	} {
		t.Run(test.resource, func(t *testing.T) {
			item := gjson.Parse(test.input)
			headers, rows, ok, err := ProjectListTable(t.Context(), "(resource) "+test.resource+" > (method) list", []gjson.Result{item})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, test.headers, headers)
			require.Equal(t, [][]string{test.row}, rows)
			require.Equal(t, test.input, item.Raw)
		})
	}
}

func TestProjectListTableMissingOptionalFields(t *testing.T) {
	for _, fields := range []string{"", `,"name":null,"status":null`, `,"name":"","status":""`} {
		item := gjson.Parse(`{"id":"proj_example","object":"organization.project","created_at":123` + fields + `}`)
		_, rows, ok, err := ProjectListTable(t.Context(), "(resource) admin.organization.projects > (method) list", []gjson.Result{item})
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, [][]string{{"proj_example", "-", "-"}}, rows)
	}
}

func TestProjectListTableEmptyPagesAndRoutes(t *testing.T) {
	for _, resource := range []string{"files", "batches", "admin.organization.projects"} {
		headers, rows, ok, err := ProjectListTable(t.Context(), "(resource) "+resource+" > (method) list", nil)
		require.NoError(t, err)
		require.True(t, ok)
		require.NotEmpty(t, headers)
		require.Empty(t, rows)
	}
	for _, operation := range []string{
		"", "batches.list", "(resource) batches > (method) retrieve", "(resource) batches > (method) list > extra",
		"(resource) organization.projects > (method) list", "(resource) beta.assistants > (method) list",
		"(resource) images.models > (method) list", "(resource) batches > (method) cancel",
		"(resource) models > (method) list",
	} {
		headers, rows, ok, err := ProjectListTable(t.Context(), operation, nil)
		require.NoError(t, err)
		require.False(t, ok, operation)
		require.Nil(t, headers)
		require.Nil(t, rows)
	}
}

func TestProjectListTablePreservesOriginalStrings(t *testing.T) {
	id := "file_" + strings.Repeat("exact-id", 1000)
	item := gjson.Parse(`{"id":"` + id + `","object":"file","filename":"日本語 é 👩‍💻\u001b[31m\n\t\r","purpose":"future_purpose","bytes":0,"status":"future_status"}`)
	_, rows, ok, err := ProjectListTable(t.Context(), "(resource) files > (method) list", []gjson.Result{item})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{id, "日本語 é 👩‍💻\x1b[31m\n\t\r", "future_purpose", "0 B", "future_status"}, rows[0])
}

func TestProjectListTableFallsBackForEntirePage(t *testing.T) {
	valid := gjson.Parse(`{"id":"batch_first","object":"batch","status":"completed"}`)
	for _, input := range []string{
		`null`, `[]`, `"batch"`, `{}`,
		`{"id":123,"object":"batch","status":"completed"}`,
		`{"id":"","object":"batch","status":"completed"}`,
		`{"id":"batch_example","object":"file","status":"completed"}`,
		`{"id":"batch_example","object":"batch"}`,
		`{"id":"batch_example","object":"batch","status":null}`,
		`{"id":"batch_example","object":"batch","status":[]}`,
		`{"id":"batch_example","object":"batch","status":"completed","warning":"Review batch."}`,
		`{"id":"batch_example","object":"batch","status":"completed","new_field":null}`,
		`{"id":"batch_example","object":"batch","status":"completed","shutdown_date":null}`,
		`{"id":"batch_example","object":"batch","status":"failed","errors":{"data":[{"message":"Review request."}]}}`,
		`{"id":"batch_first","id":"batch_second","object":"batch","status":"completed"}`,
		`{"id":"batch_example","object":"batch","status":"completed","created_at":1,"created_at":2}`,
		`{"id":"batch_example","object":"batch","status":"completed"`,
		`{"id":"batch_example","object":"batch" "status":"completed"}`,
	} {
		t.Run(input, func(t *testing.T) {
			item := gjson.Parse(input)
			headers, rows, ok, err := ProjectListTable(t.Context(), "(resource) batches > (method) list", []gjson.Result{valid, item, valid})
			require.NoError(t, err)
			require.False(t, ok)
			require.Nil(t, headers)
			require.Nil(t, rows, "Do not return the valid prefix as a partial table.")
			require.Equal(t, input, item.Raw)
		})
	}
}

func TestProjectListTablePreservesErrorsAndMalformedFields(t *testing.T) {
	for _, test := range []struct{ resource, input string }{
		{"files", `{"id":"file_example","object":"file","filename":"input.txt","purpose":"batch","bytes":0,"status":"error","status_details":"Review file."}`},
		{"files", `{"id":"file_example","object":"file","filename":"input.txt","purpose":"batch","bytes":0,"status":"processed","status_details":[]}`},
		{"files", `{"id":"file_example","object":"file","filename":"input.txt","purpose":"batch","bytes":"1024","status":"processed"}`},
		{"files", `{"id":"file_example","object":"file","filename":"input.txt","purpose":"batch","bytes":-1,"status":"processed"}`},
		{"files", `{"id":"file_example","object":"file","filename":"input.txt","purpose":"batch","bytes":1.5,"status":"processed"}`},
		{"files", `{"id":"file_example","object":"file","filename":"input.txt","purpose":"batch","bytes":18446744073709551616,"status":"processed"}`},
		{"files", `{"id":"file_example","object":"file","filename":{},"purpose":"batch","bytes":0,"status":"processed"}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"failed","errors":{"data":[{"message":"Review request."}]}}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","errors":[]}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","errors":""}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","error_file_id":"file_errors"}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","request_counts":{"total":3,"completed":2,"failed":1}}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","request_counts":{"total":3,"completed":3,"failed":0,"new_count":1}}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","request_counts":{"total":3,"completed":3,"failed":0,"failed":1}}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","request_counts":{"total":3,"completed":3,"failed":"0"}}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","request_counts":{"total":3,"completed":3}}`},
		{"batches", `{"id":"batch_example","object":"batch","status":"completed","request_counts":[]}`},
		{"admin.organization.projects", `{"id":"proj_example","object":"organization.project","name":[],"status":"active"}`},
		{"admin.organization.projects", `{"id":"proj_example","object":"organization.project","name":"Synthetic","status":false}`},
		{"admin.organization.projects", `{"id":"proj_example","object":"organization.project","name":"Synthetic","status":"active","warning":"Review project."}`},
		{"admin.organization.projects", `{"id":"proj_example","object":"organization.project","name":"First","name":"Second","status":"active"}`},
	} {
		t.Run(test.resource+test.input, func(t *testing.T) {
			item := gjson.Parse(test.input)
			headers, rows, ok, err := ProjectListTable(t.Context(), "(resource) "+test.resource+" > (method) list", []gjson.Result{item})
			require.NoError(t, err)
			require.False(t, ok)
			require.Nil(t, headers)
			require.Nil(t, rows)
			require.Equal(t, test.input, item.Raw)
		})
	}
}

func TestProjectListTableByteSizes(t *testing.T) {
	for _, test := range []struct{ bytes, want string }{
		{"0", "0 B"}, {"1", "1 B"}, {"1023", "1023 B"}, {"1024", "1 KiB"},
		{"1536", "1.5 KiB"}, {"1234", "1.2 KiB"}, {"1048576", "1 MiB"},
		{"9007199254740993", "8 PiB"}, {"18446744073709551615", "16 EiB"},
	} {
		t.Run(test.bytes, func(t *testing.T) {
			input := `{"id":"file_size","object":"file","filename":"size.bin","purpose":"user_data","status":"processed","bytes":` + test.bytes + `}`
			item := gjson.Parse(input)
			_, rows, ok, err := ProjectListTable(t.Context(), "(resource) files > (method) list", []gjson.Result{item})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, test.want, rows[0][3])
			require.Equal(t, test.bytes, item.Get("bytes").Raw)
			require.Equal(t, input, item.Raw)
		})
	}
}
