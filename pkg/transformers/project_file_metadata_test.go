package transformers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectFileMetadataUTCAndCompleteFields(t *testing.T) {
	input := `{"object":"file","id":"file-full-copyable-id","filename":"upload space.txt","purpose":"user_data","bytes":9007199254740993,"created_at":1700000000,"expires_at":1700003600,"status":"error","status_details":"synthetic failure","future":{"number":9007199254740995,"nullable":null},"unknown_time":1700000000}`
	value := gjson.Parse(input)
	got, err := ProjectFileMetadata(t.Context(), value)
	require.NoError(t, err)
	require.Equal(t, `{"object":"file","id":"file-full-copyable-id","filename":"upload space.txt","purpose":"user_data","bytes":9007199254740993,"created_at":"2023-11-14T22:13:20Z","expires_at":"2023-11-14T23:13:20Z","status":"error","status_details":"synthetic failure","future":{"number":9007199254740995,"nullable":null},"unknown_time":1700000000}`, got.Raw)
	require.Equal(t, input, value.Raw, "projection must not mutate original machine data")
}

func TestProjectFileMetadataPreservesInvalidAndNullTimestamps(t *testing.T) {
	for _, timestamp := range []string{`null`, `"1700000000"`, `1.5`, `1e3`, `true`, `{}`, `[]`, `9223372036854775808`, `9223372036854775807`, `253402300800`, `-62167219201`} {
		input := `{"object":"file","bytes":null,"created_at":` + timestamp + `,"expires_at":` + timestamp + `,"status_details":null}`
		got, err := ProjectFileMetadata(t.Context(), gjson.Parse(input))
		require.NoError(t, err)
		require.Equal(t, input, got.Raw)
	}
	for _, tc := range []struct{ timestamp, want string }{{"0", "1970-01-01T00:00:00Z"}, {"-1", "1969-12-31T23:59:59Z"}, {"253402300799", "9999-12-31T23:59:59Z"}} {
		got, err := ProjectFileMetadata(t.Context(), gjson.Parse(`{"object":"file","created_at":`+tc.timestamp+`}`))
		require.NoError(t, err)
		require.Equal(t, tc.want, got.Get("created_at").Str)
	}
}

func TestProjectFileMetadataPreservesOtherShapesAndDuplicateFields(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `"file"`, `{"object":"batch","created_at":1700000000}`, `{"created_at":1700000000}`, `{"object":"file","created_at":1700000000`} {
		got, err := ProjectFileMetadata(t.Context(), gjson.Parse(input))
		require.NoError(t, err)
		require.Equal(t, input, got.Raw)
	}
	input := `{"object":"file","future":1,"future":9007199254740993,"created_at":0}`
	got, err := ProjectFileMetadata(t.Context(), gjson.Parse(input))
	require.NoError(t, err)
	require.Equal(t, `{"object":"file","future":1,"future":9007199254740993,"created_at":"1970-01-01T00:00:00Z"}`, got.Raw)
}

func TestProjectFileMetadataCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	input := gjson.Parse(`{"object":"file","created_at":1700000000}`)
	got, err := ProjectFileMetadata(ctx, input)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, gjson.Result{}, got)
	require.Equal(t, `{"object":"file","created_at":1700000000}`, input.Raw)
}
