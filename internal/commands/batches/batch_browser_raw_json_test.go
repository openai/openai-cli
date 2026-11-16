package batches

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchBrowserRawJSONPreservesFieldsNumbersAndControls(t *testing.T) {
	raw := `{"custom_id":"one","input":"é` + "\u202e\u009b" + `\n\"quoted\"","empty":[],"object":{},"large_number":9007199254740993,"duplicate":1,"duplicate":2}`
	var out bytes.Buffer
	require.NoError(t, writeBatchBrowserRawJSON(t.Context(), &out, func(w io.Writer) error { _, err := io.WriteString(w, raw); return err }))
	require.True(t, json.Valid(out.Bytes()))
	require.NotContains(t, out.String(), "\u202e")
	require.NotContains(t, out.String(), "\u009b")
	require.Contains(t, out.String(), "9007199254740993")
	require.Equal(t, 2, strings.Count(out.String(), `"duplicate"`))
	var got, want any
	require.NoError(t, json.Unmarshal([]byte(raw), &want))
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, want, got)
	require.Contains(t, out.String(), "\n  \"custom_id\": \"one\"")
}

func TestBatchBrowserRawJSONLargeRecordFailureAndCancellation(t *testing.T) {
	write := func(w io.Writer) error {
		if _, err := io.WriteString(w, `{"input":"`); err != nil {
			return err
		}
		for range 100 {
			if _, err := io.WriteString(w, strings.Repeat("a", 32*1024)); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, `","max_output_tokens":32}`)
		return err
	}
	var out bytes.Buffer
	require.NoError(t, writeBatchBrowserRawJSON(t.Context(), &out, write))
	require.True(t, json.Valid(out.Bytes()))
	require.Contains(t, out.String(), `"max_output_tokens": 32`)
	err := writeBatchBrowserRawJSON(t.Context(), batchDownloadFailWriter{}, write)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, writeBatchBrowserRawJSON(ctx, io.Discard, write), context.Canceled)
	failure := errors.New("synthetic source failure")
	require.ErrorIs(t, writeBatchBrowserRawJSON(t.Context(), io.Discard, func(io.Writer) error { return failure }), failure)
}
