package batches

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchRecordVisitorKeepsDuplicateGroupsSeparate(t *testing.T) {
	store, err := newBatchRecordStore(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.close()) })
	results := "{\"custom_id\":\"same\",\"output\":1}\n{\"custom_id\":\"same\",\"output\":2}\n"
	inputs := "{\"custom_id\":\"same\",\"input\":1}\n{\"custom_id\":\"same\",\"input\":2}\n{\"custom_id\":\"pending\",\"input\":3}\n"
	require.NoError(t, store.readJSONL(strings.NewReader(results), "output", "", false, true))
	require.NoError(t, store.readJSONL(strings.NewReader(inputs), "input", "", true, true))
	var before, after bytes.Buffer
	require.NoError(t, store.writeJSON(&before, "batch-demo", "output", "file-demo", true))
	var kinds []string
	var values []gjson.Result
	require.NoError(t, store.visitRecords("output", true, func(item RecordItem) error {
		var data bytes.Buffer
		if err := item.WriteJSON(&data); err != nil {
			return err
		}
		kinds = append(kinds, item.Kind)
		values = append(values, gjson.Parse(data.String()))
		return nil
	}))
	require.Equal(t, []string{"group", "matched_result", "matched_result", "matched_input", "matched_input", "group", "matched_input"}, kinds)
	require.Equal(t, "ambiguous", values[0].Get("match").Str)
	require.Equal(t, int64(2), values[0].Get("input_count").Int())
	require.Equal(t, int64(2), values[0].Get("result_count").Int())
	require.Equal(t, "1", values[1].Get("output").Raw)
	require.Equal(t, "2", values[2].Get("output").Raw)
	require.Equal(t, "missing_result", values[5].Get("match").Str)
	require.NoError(t, store.writeJSON(&after, "batch-demo", "output", "file-demo", true))
	require.Equal(t, before.String(), after.String())
}

func TestBatchRecordVisitorStopsOnFailure(t *testing.T) {
	store, err := newBatchRecordStore(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.close()) })
	require.NoError(t, store.readJSONL(strings.NewReader("{}\n{}\n"), "output", "", false, false))
	failure, count := errors.New("synthetic visitor failure"), 0
	err = store.visitRecords("output", false, func(item RecordItem) error { count++; return failure })
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, count)
}
