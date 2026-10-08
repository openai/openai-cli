package transformers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectModelNamesPreservesIDsAndSortsSelectedItems(t *testing.T) {
	items := []gjson.Result{
		gjson.Parse(`{"id":"synthetic_z","object":"model"}`),
		gjson.Parse(`{"id":"synthetic_A","object":"model"}`),
		gjson.Parse(`{"id":"synthetic_a","object":"model"}`),
		gjson.Parse(`{"id":"synthetic_A","object":"model"}`),
	}
	original := append([]gjson.Result(nil), items...)
	names, supported, err := ProjectModelNames(t.Context(), items, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []string{"synthetic_A", "synthetic_A", "synthetic_a", "synthetic_z"}, names)
	require.Equal(t, original, items, "Projection must not reorder or modify the original records.")

	// The caller applies max-items before projection. Sort only those records.
	names, supported, err = ProjectModelNames(t.Context(), items[:2], false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []string{"synthetic_A", "synthetic_z"}, names)

	names, supported, err = ProjectModelNames(t.Context(), items, true)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []string{"synthetic_z", "synthetic_a", "synthetic_A", "synthetic_A"}, names)
	require.Equal(t, original, items)
}

func TestProjectModelNamesIgnoresUnfamiliarMetadata(t *testing.T) {
	metadata := strings.Repeat("synthetic-metadata-", 256*1024)
	items := []gjson.Result{
		gjson.Parse(`{"id":"synthetic_second","object":"model","shutdown_date":"2027-01-01","owned_by":null,"created":{},"unknown":{"id":"nested","object":"file","payload":"` + metadata + `"}}`),
		gjson.Parse(`{"unknown":[false,null,1],"id":"synthetic_first","object":"model","warning":"synthetic notice","unknown":"another value"}`),
	}
	original := append([]gjson.Result(nil), items...)
	names, supported, err := ProjectModelNames(t.Context(), items, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []string{"synthetic_first", "synthetic_second"}, names)
	require.Equal(t, original, items)
}

func TestProjectModelNamesPreservesUnicodeAndControls(t *testing.T) {
	id := "  synthetic-日本語-e\u0301-👩‍💻-\x1b[31m\n\t\r\x00  " + strings.Repeat("long-id-", 4096)
	encoded, err := json.Marshal(map[string]string{"id": id, "object": "model"})
	require.NoError(t, err)
	names, supported, err := ProjectModelNames(t.Context(), []gjson.Result{gjson.ParseBytes(encoded)}, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []string{id}, names, "Only the renderer may escape terminal controls.")
}

func TestProjectModelNamesRejectsAmbiguousOrMalformedIdentity(t *testing.T) {
	valid := gjson.Parse(`{"id":"synthetic_valid","object":"model"}`)
	for _, test := range []struct{ name, input string }{
		{"null", `null`},
		{"array", `[]`},
		{"string", `"model"`},
		{"missing_id", `{"object":"model"}`},
		{"missing_object", `{"id":"synthetic"}`},
		{"empty_id", `{"id":"","object":"model"}`},
		{"number_id", `{"id":123,"object":"model"}`},
		{"boolean_id", `{"id":true,"object":"model"}`},
		{"null_id", `{"id":null,"object":"model"}`},
		{"array_id", `{"id":[],"object":"model"}`},
		{"object_id", `{"id":{},"object":"model"}`},
		{"wrong_object", `{"id":"synthetic","object":"file"}`},
		{"null_object", `{"id":"synthetic","object":null}`},
		{"object_object", `{"id":"synthetic","object":{"name":"model"}}`},
		{"duplicate_id", `{"id":"synthetic","id":"synthetic","object":"model"}`},
		{"conflicting_id", `{"id":"synthetic","id":"different","object":"model"}`},
		{"escaped_duplicate_id", `{"id":"synthetic","i\u0064":"synthetic","object":"model"}`},
		{"duplicate_object", `{"id":"synthetic","object":"model","object":"model"}`},
		{"conflicting_object", `{"id":"synthetic","object":"model","object":"file"}`},
		{"escaped_duplicate_object", `{"id":"synthetic","object":"model","obj\u0065ct":"model"}`},
		{"missing_brace", `{"id":"synthetic","object":"model"`},
		{"missing_comma", `{"id":"synthetic" "object":"model"}`},
		{"invalid_metadata", `{"id":"synthetic","object":"model","unknown":wat}`},
		{"invalid_escape", `{"id":"synthetic\x1b","object":"model"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := gjson.Parse(test.input)
			original := item
			names, supported, err := ProjectModelNames(t.Context(), []gjson.Result{valid, item, valid}, false)
			require.NoError(t, err)
			require.False(t, supported)
			require.Nil(t, names, "Fallback must preserve the whole page, not a valid prefix.")
			require.Equal(t, original, item)
		})
	}
}

func TestProjectModelNamesEmptyAndCancellation(t *testing.T) {
	names, supported, err := ProjectModelNames(t.Context(), nil, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Empty(t, names)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	names, supported, err = ProjectModelNames(ctx, nil, false)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, supported)
	require.Nil(t, names)

	items := []gjson.Result{
		gjson.Parse(`{"id":"synthetic_second","object":"model","unknown":true}`),
		gjson.Parse(`{"id":"synthetic_first","object":"model"}`),
	}
	for _, after := range []int32{2, 3, 5, 9, 13, 14} {
		ctx, cancel := context.WithCancel(t.Context())
		polls := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
		names, supported, err := ProjectModelNames(polls, items, false)
		cancel()
		require.ErrorIs(t, err, context.Canceled, "cancel after %d checks", after)
		require.False(t, supported)
		require.Nil(t, names)
	}
}
