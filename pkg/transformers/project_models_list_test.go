package transformers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectModelsListPreservesIDsAndSortsSelectedItems(t *testing.T) {
	items := []gjson.Result{
		gjson.Parse(`{"id":"synthetic_z","object":"model"}`),
		gjson.Parse(`{"id":"synthetic_A","object":"model"}`),
		gjson.Parse(`{"id":"synthetic_a","object":"model"}`),
		gjson.Parse(`{"id":"synthetic_A","object":"model"}`),
	}
	original := append([]gjson.Result(nil), items...)
	names, supported, err := ProjectModelsList(t.Context(), items, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []ModelListRow{{ID: "synthetic_A"}, {ID: "synthetic_A"}, {ID: "synthetic_a"}, {ID: "synthetic_z"}}, names)
	require.Equal(t, original, items, "Projection must not reorder or modify the original records.")

	// The caller applies max-items before projection. Sort only those records.
	names, supported, err = ProjectModelsList(t.Context(), items[:2], false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []ModelListRow{{ID: "synthetic_A"}, {ID: "synthetic_z"}}, names)

	names, supported, err = ProjectModelsList(t.Context(), items, true)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []ModelListRow{{ID: "synthetic_z"}, {ID: "synthetic_a"}, {ID: "synthetic_A"}, {ID: "synthetic_A"}}, names)
	require.Equal(t, original, items)
}

func TestProjectModelsListIgnoresUnfamiliarMetadata(t *testing.T) {
	metadata := strings.Repeat("synthetic-metadata-", 256*1024)
	items := []gjson.Result{
		gjson.Parse(`{"id":"synthetic_second","object":"model","shutdown_date":"2027-01-01","owned_by":null,"created":{},"unknown":{"id":"nested","object":"file","payload":"` + metadata + `"}}`),
		gjson.Parse(`{"unknown":[false,null,1],"id":"synthetic_first","object":"model","warning":"synthetic notice","unknown":"another value"}`),
	}
	original := append([]gjson.Result(nil), items...)
	names, supported, err := ProjectModelsList(t.Context(), items, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []ModelListRow{{ID: "synthetic_first"}, {ID: "synthetic_second"}}, names)
	require.Equal(t, original, items)
}

func TestProjectModelsListPreservesUnicodeAndControls(t *testing.T) {
	id := "  synthetic-日本語-e\u0301-👩‍💻-\x1b[31m\n\t\r\x00  " + strings.Repeat("long-id-", 4096)
	owner := "owner-研究-e\u0301-\x1b[31m\n\t\r\x00"
	encoded, err := json.Marshal(map[string]string{"id": id, "object": "model", "owned_by": owner})
	require.NoError(t, err)
	names, supported, err := ProjectModelsList(t.Context(), []gjson.Result{gjson.ParseBytes(encoded)}, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, []ModelListRow{{ID: id, Owner: &owner}}, names, "Only the renderer may escape terminal controls.")
}

func TestProjectModelsListKeepsStableOwnerAssociation(t *testing.T) {
	items := []gjson.Result{
		gjson.Parse(`{"id":"model-z","object":"model","owned_by":"z-owner"}`),
		gjson.Parse(`{"id":"model-a","object":"model","owned_by":"first-a-owner"}`),
		gjson.Parse(`{"id":"model-a","object":"model","owned_by":"second-a-owner"}`),
	}
	original := append([]gjson.Result(nil), items...)
	for _, descending := range []bool{false, true} {
		rows, supported, err := ProjectModelsList(t.Context(), items, descending)
		require.NoError(t, err)
		require.True(t, supported)
		order := []int{1, 2, 0}
		if descending {
			order = []int{0, 1, 2}
		}
		for index, input := range order {
			require.Equal(t, items[input].Get("id").Str, rows[index].ID)
			require.NotNil(t, rows[index].Owner)
			require.Equal(t, items[input].Get("owned_by").Str, *rows[index].Owner)
		}
		require.Equal(t, original, items)
	}
}

func TestProjectModelsListUnknownOwnerIsNotInvented(t *testing.T) {
	for _, ownerFields := range []string{
		``, `,"owned_by":null`, `,"owned_by":""`, `,"owned_by":123`, `,"owned_by":false`,
		`,"owned_by":[]`, `,"owned_by":{"name":"synthetic"}`,
		`,"owned_by":"first","owned_by":"second"`,
		`,"owned_by":"same","owned_by":"same"`,
		`,"owned_by":"first","owned_\u0062y":"second"`,
		`,"owned_by":null,"owned_by":"second","owned_by":"third"`,
	} {
		t.Run(ownerFields, func(t *testing.T) {
			item := gjson.Parse(`{"id":"model-a","object":"model"` + ownerFields + `}`)
			rows, supported, err := ProjectModelsList(t.Context(), []gjson.Result{item}, false)
			require.NoError(t, err)
			require.True(t, supported)
			require.Equal(t, []ModelListRow{{ID: "model-a"}}, rows)
		})
	}
	for _, owner := range []string{"(unknown)", " ", "null", "synthetic-owner"} {
		encoded, err := json.Marshal(map[string]string{"id": "model-a", "object": "model", "owned_by": owner})
		require.NoError(t, err)
		rows, supported, err := ProjectModelsList(t.Context(), []gjson.Result{gjson.ParseBytes(encoded)}, false)
		require.NoError(t, err)
		require.True(t, supported)
		require.NotNil(t, rows[0].Owner, "A real owner value is distinct from unavailable owner information.")
		require.Equal(t, owner, *rows[0].Owner)
	}
}

func TestProjectModelsListRejectsAmbiguousOrMalformedIdentity(t *testing.T) {
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
			names, supported, err := ProjectModelsList(t.Context(), []gjson.Result{valid, item, valid}, false)
			require.NoError(t, err)
			require.False(t, supported)
			require.Nil(t, names, "Fallback must preserve the whole page, not a valid prefix.")
			require.Equal(t, original, item)
		})
	}
}

func TestProjectModelsListEmptyAndCancellation(t *testing.T) {
	names, supported, err := ProjectModelsList(t.Context(), nil, false)
	require.NoError(t, err)
	require.True(t, supported)
	require.Empty(t, names)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	names, supported, err = ProjectModelsList(ctx, nil, false)
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
		names, supported, err := ProjectModelsList(polls, items, false)
		cancel()
		require.ErrorIs(t, err, context.Canceled, "cancel after %d checks", after)
		require.False(t, supported)
		require.Nil(t, names)
	}
}
