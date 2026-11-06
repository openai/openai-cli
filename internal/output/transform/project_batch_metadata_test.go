package transform

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSummaryBatchRoutes(t *testing.T) {
	value := gjson.Parse(`{"id":"batch_example","object":"batch","status":"validating","created_at":0}`)
	for _, method := range []string{"retrieve", "list", "create", "cancel", "wait", "download", "update", "delete"} {
		for _, kind := range []OutputKind{OutputUnspecified, OutputResponse, OutputPageItem, OutputStreamEvent} {
			route := Route{"(resource) batches > (method) " + method, kind}
			got, _, err := SummarizeResource(t.Context(), value, route)
			require.NoError(t, err)
			want := kind == OutputResponse && (method == "retrieve" || method == "create" || method == "cancel") || kind == OutputPageItem && method == "list"
			require.Equal(t, want, got.Exists(), "%+v", route)
		}
	}
}

func TestSummaryBatchLifecycle(t *testing.T) {
	input := `{"id":"batch_exact:資料-001","object":"batch","completed_at":3661,"expired_at":6,"created_at":0,"failed_at":5,"cancelled_at":4,"cancelling_at":3,"finalizing_at":2,"in_progress_at":1,"status":"completed","completion_window":"24h","request_counts":{"total":3,"completed":2,"failed":1},"input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error","errors":{"data":[{"code":"example","line":9007199254740993,"message":"detail\u001b[31m"}]},"endpoint":"/v1/responses","model":"model_example","expires_at":86400,"usage":{"total_tokens":9007199254740993},"metadata":{"label":"資料\u001b[31m"}}`
	value := gjson.Parse(input)
	got, omitted, err := SummarizeResource(t.Context(), value, Route{"(resource) batches > (method) retrieve", OutputResponse})
	require.NoError(t, err)
	require.True(t, omitted)
	require.Equal(t, "1970-01-01T00:00:00Z", got.Get("created_at").Str)
	require.Equal(t, "1970-01-01T01:01:01Z", got.Get("completed_at").Str)
	require.Equal(t, "1970-01-02T00:00:00Z", got.Get("expires_at").Str)
	require.Equal(t, "1h 1m 1s", got.Get("completion_duration").Str)
	previous := -1
	for _, key := range []string{"created_at", "in_progress_at", "finalizing_at", "cancelling_at", "cancelled_at", "failed_at", "expired_at", "completed_at", "expires_at"} {
		index := strings.Index(got.Raw, `"`+key+`":`)
		require.Greater(t, index, previous, key)
		previous = index
	}
	for _, key := range []string{"id", "status", "completion_window", "request_counts", "input_file_id", "output_file_id", "error_file_id", "errors", "endpoint", "model", "usage", "metadata"} {
		require.Equal(t, value.Get(key).Raw, got.Get(key).Raw, key)
	}
	require.False(t, got.Get("object").Exists())
	require.Equal(t, input, value.Raw)
}

func TestSummaryBatchTimestampBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, fields, created, completed, duration string
	}{
		{"zero", `"created_at":0,"completed_at":0`, "1970-01-01T00:00:00Z", "1970-01-01T00:00:00Z", "0s"},
		{"negative", `"created_at":-1,"completed_at":0`, "1969-12-31T23:59:59Z", "1970-01-01T00:00:00Z", "1s"},
		{"date range", `"created_at":-62167219200,"completed_at":253402300799`, "0000-01-01T00:00:00Z", "9999-12-31T23:59:59Z", "3652424d 23h 59m 59s"},
		{"reversed", `"created_at":1,"completed_at":0`, "1970-01-01T00:00:01Z", "1970-01-01T00:00:00Z", ""},
		{"missing creation", `"completed_at":0`, "", "1970-01-01T00:00:00Z", ""},
		{"null completion", `"created_at":0,"completed_at":null`, "1970-01-01T00:00:00Z", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := gjson.Parse(`{"id":"batch_example","object":"batch","status":"completed",` + tc.fields + `}`)
			got, _, err := SummarizeResource(t.Context(), value, Route{"(resource) batches > (method) retrieve", OutputResponse})
			require.NoError(t, err)
			require.True(t, got.Exists())
			require.Equal(t, tc.created, got.Get("created_at").Str)
			require.Equal(t, tc.completed, got.Get("completed_at").Str)
			require.Equal(t, tc.duration, got.Get("completion_duration").Str)
		})
	}
	// Equal timestamps retain lifecycle order even when API keys arrive reversed.
	value := gjson.Parse(`{"id":"batch_example","object":"batch","status":"cancelled","cancelled_at":0,"cancelling_at":0,"completed_at":0,"created_at":0}`)
	got, _, err := SummarizeResource(t.Context(), value, Route{"(resource) batches > (method) retrieve", OutputResponse})
	require.NoError(t, err)
	require.Equal(t, `{"id":"batch_example","status":"cancelled","created_at":"1970-01-01T00:00:00Z","completed_at":"1970-01-01T00:00:00Z","cancelling_at":"1970-01-01T00:00:00Z","cancelled_at":"1970-01-01T00:00:00Z"}`, got.Raw)
	require.False(t, got.Get("completion_duration").Exists())
}

func TestSummaryBatchFallback(t *testing.T) {
	for _, field := range strings.Fields("created_at in_progress_at finalizing_at completed_at failed_at cancelling_at cancelled_at expired_at expires_at") {
		for _, raw := range []string{`"17"`, `""`, `[]`, `{}`, `false`, `1.5`, `1e3`, `-62167219201`, `253402300800`, `9223372036854775807`, `-9223372036854775808`, `9223372036854775808`} {
			value := gjson.Parse(`{"id":"batch_example","object":"batch","status":"completed","` + field + `":` + raw + `}`)
			got, omitted, err := SummarizeResource(t.Context(), value, Route{"(resource) batches > (method) retrieve", OutputResponse})
			require.NoError(t, err)
			require.False(t, got.Exists(), "%s=%s", field, raw)
			require.False(t, omitted)
		}
	}
	for _, fields := range []string{
		`"future_field":null`, `"completion_duration":"server detail"`,
		`"created_at":0,"created_at":1`, `"created_at":0,"created\u005fat":1`,
		`"status":"future_status"`, `"status":17`, `"status":false`, `"status":[]`,
	} {
		value := gjson.Parse(`{"id":"batch_example","object":"batch",` + fields + `}`)
		got, omitted, err := SummarizeResource(t.Context(), value, Route{"(resource) batches > (method) retrieve", OutputResponse})
		require.NoError(t, err)
		require.False(t, got.Exists(), fields)
		require.False(t, omitted)
	}
	for _, raw := range []string{
		`{"id":"batch_example","object":"batch","created_at":0`,
		`{"id":"batch_example","object":"batch","created_at":0,}`,
		`{"id":"batch_example","object":"batch","created_at":0} true`,
	} {
		got, omitted, err := SummarizeResource(t.Context(), gjson.Parse(raw), Route{"(resource) batches > (method) retrieve", OutputResponse})
		require.NoError(t, err)
		require.False(t, got.Exists(), raw)
		require.False(t, omitted)
	}
}

func TestSummaryBatchEmptyFieldsAndCancellation(t *testing.T) {
	value := gjson.Parse(`{"id":"batch_example","object":"batch","status":"validating","created_at":0,"completed_at":null,"metadata":{},"errors":null,"output_file_id":"","usage":{},"request_counts":{"total":0,"completed":0,"failed":0}}`)
	route := Route{"(resource) batches > (method) create", OutputResponse}
	got, omitted, err := SummarizeResource(t.Context(), value, route)
	require.NoError(t, err)
	require.True(t, omitted)
	require.Equal(t, `{"id":"batch_example","status":"validating","created_at":"1970-01-01T00:00:00Z","request_counts":{"total":0,"completed":0,"failed":0}}`, got.Raw)
	for _, after := range []int32{1, 4, 12, 25} {
		ctx, cancel := context.WithCancel(t.Context())
		polls := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
		got, omitted, err := SummarizeResource(polls, value, route)
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, got.Exists())
		require.False(t, omitted)
	}
}
