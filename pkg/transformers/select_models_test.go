package transformers

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSelectModelsFiltersSortsThenLimits(t *testing.T) {
	items := []gjson.Result{
		gjson.Parse(`{"object":"model","id":"synthetic-z","metadata":{"id":"synthetic-a"}}`),
		gjson.Parse(`{ "object": "model", "id": "synthetic-c", "unknown": 1e2 }`),
		gjson.Parse(`{"id":"synthetic-a","object":"model","unknown":9007199254740993}`),
		gjson.Parse(`{"object":"model","id":"synthetic-a","unknown":-0.00}`),
		gjson.Parse(`{"object":"model","id":"synthetic-b","unknown":"synthetic-c"}`),
	}
	original := append([]gjson.Result(nil), items...)
	exact := "synthetic-a"
	pattern := regexp.MustCompile(`^synthetic-[ac]$`)
	for _, test := range []struct {
		name      string
		selection ModelSelection
		indices   []int
	}{
		{"ascending", ModelSelection{Limit: -1}, []int{2, 3, 4, 1, 0}},
		{"descending", ModelSelection{Descending: true, Limit: -1}, []int{0, 1, 4, 2, 3}},
		{"regex_then_sort_then_limit", ModelSelection{Pattern: pattern, Limit: 2}, []int{2, 3}},
		{"regex_then_descending_then_limit", ModelSelection{Pattern: pattern, Descending: true, Limit: 2}, []int{1, 2}},
		{"exact_keeps_duplicates", ModelSelection{ExactID: &exact, Limit: -1}, []int{2, 3}},
		{"exact_then_limit", ModelSelection{ExactID: &exact, Limit: 1}, []int{2}},
		{"zero", ModelSelection{Pattern: pattern, Limit: 0}, nil},
		{"negative_unlimited", ModelSelection{Pattern: pattern, Limit: -9}, []int{2, 3, 1}},
		{"large_limit", ModelSelection{Pattern: pattern, Limit: math.MaxInt64}, []int{2, 3, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected, supported, err := SelectModels(t.Context(), items, test.selection)
			require.NoError(t, err)
			require.True(t, supported)
			require.Len(t, selected, len(test.indices))
			for index, originalIndex := range test.indices {
				require.Equal(t, original[originalIndex], selected[index], "Keep original JSON bytes and equal-ID order.")
			}
			require.Equal(t, original, items, "Selection must not mutate its input.")
		})
	}
}

func TestSelectModelsPreservesExactIdentityAndUnknownMetadata(t *testing.T) {
	id := "synthetic.* 日本語 e\u0301 👩‍💻\n\t\x1b[31m\x00" + strings.Repeat("long-id-", 4096)
	metadata := strings.Repeat("synthetic-metadata-", 256*1024)
	encoded, err := json.Marshal(map[string]any{
		"id": id, "object": "model", "shutdown_date": "2030-01-01",
		"unknown": map[string]any{"payload": metadata, "object": "file", "id": "nested"},
	})
	require.NoError(t, err)
	item := gjson.ParseBytes(encoded)
	for _, selection := range []ModelSelection{
		{ExactID: &id, Limit: -1},
		{Pattern: regexp.MustCompile(`(?s)^synthetic\.\* 日本語 .*long-id-$`), Limit: -1},
	} {
		selected, supported, err := SelectModels(t.Context(), []gjson.Result{item}, selection)
		require.NoError(t, err)
		require.True(t, supported)
		require.Equal(t, []gjson.Result{item}, selected)
		require.Equal(t, id, selected[0].Get("id").Str)
		require.Equal(t, metadata, selected[0].Get("unknown.payload").Str)
	}

	missing := "synthetic"
	for _, selection := range []ModelSelection{
		{ExactID: &missing, Limit: -1},
		{Pattern: regexp.MustCompile(`^nested$`), Limit: -1},
	} {
		selected, supported, err := SelectModels(t.Context(), []gjson.Result{item}, selection)
		require.NoError(t, err)
		require.True(t, supported)
		require.Empty(t, selected, "Filters match only the complete top-level ID.")
	}
}

func TestSelectModelsRejectsMalformedIdentityBeforeFiltering(t *testing.T) {
	valid := gjson.Parse(`{"object":"model","id":"synthetic-match"}`)
	selection := ModelSelection{Pattern: regexp.MustCompile(`^synthetic-match$`), Limit: 1}
	for _, input := range []string{
		`null`, `[]`, `{}`, `{"object":"model","id":123}`,
		`{"object":"model","id":""}`, `{"object":"file","id":"synthetic-other"}`,
		`{"object":"model","id":"synthetic-other","id":"synthetic-other"}`,
		`{"object":"model","id":"synthetic-other","i\u0064":"synthetic-other"}`,
		`{"object":"model","object":"model","id":"synthetic-other"}`,
		`{"object":"model","obj\u0065ct":"model","id":"synthetic-other"}`,
		`{"object":"model","id":"synthetic-other"`,
		`{"object":"model","id":"synthetic-other","unknown":wat}`,
	} {
		t.Run(input, func(t *testing.T) {
			items := []gjson.Result{valid, gjson.Parse(input), valid}
			original := append([]gjson.Result(nil), items...)
			selected, supported, err := SelectModels(t.Context(), items, selection)
			require.NoError(t, err)
			require.False(t, supported)
			require.Nil(t, selected, "Do not hide malformed records through filtering or a result limit.")
			require.Equal(t, original, items)
		})
	}
}

func TestSelectModelsEmptyAndCancellation(t *testing.T) {
	selected, supported, err := SelectModels(t.Context(), nil, ModelSelection{Limit: -1})
	require.NoError(t, err)
	require.True(t, supported)
	require.Empty(t, selected)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	selected, supported, err = SelectModels(ctx, nil, ModelSelection{Limit: -1})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, supported)
	require.Nil(t, selected)

	items := []gjson.Result{
		gjson.Parse(`{"id":"synthetic-second","object":"model","unknown":true}`),
		gjson.Parse(`{"id":"synthetic-first","object":"model"}`),
	}
	for _, after := range []int32{2, 3, 5, 9, 13, 14, 15, 16, 17} {
		ctx, cancel := context.WithCancel(t.Context())
		polls := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
		selected, supported, err := SelectModels(polls, items, ModelSelection{Limit: -1})
		cancel()
		require.ErrorIs(t, err, context.Canceled, "cancel after %d checks", after)
		require.False(t, supported)
		require.Nil(t, selected)
	}
}
