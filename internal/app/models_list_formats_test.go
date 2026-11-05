package app

import (
	"bytes"
	"encoding/json"
	"testing"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestModelsListExplicitJSONKeepsCompleteRecords(t *testing.T) {
	const data = `[{"id":"model-a","object":"model","created":123,"owned_by":"openai"},` +
		`{"id":"model-b","object":"model","created":456,"owned_by":"owner-\u001b[31m"}]`
	source, _ := staticModelIterator(t, data, -1, nil)
	var output, diagnostics bytes.Buffer
	opts := ShowJSONOpts{Context: t.Context(), Stdout: &output, Stderr: &diagnostics,
		Operation: "(resource) models > (method) list", OutputKind: OutputPageItem,
		Format: "json", ExplicitFormat: true}

	require.NoError(t, ShowJSONIterator(source, -1, opts))
	var records []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &records))
	require.Equal(t, []map[string]any{
		{"id": "model-a", "object": "model", "created": float64(123), "owned_by": "openai"},
		{"id": "model-b", "object": "model", "created": float64(456), "owned_by": "owner-\x1b[31m"},
	}, records)
	require.Empty(t, diagnostics.String())
	require.NotContains(t, output.String(), "\x1b", "JSON must encode terminal controls")
	require.False(t, opts.RawOutput, "presentation must not mutate caller options")

	// Ordinary labeled output retains its existing summary contract.
	opts.Format = "auto"
	opts.ExplicitFormat = false
	ordinary, err := renderListNavigationLabels(opts, gjson.Parse(data).Array(), true)
	require.NoError(t, err)
	require.NotContains(t, ordinary, "Created:")
	require.Contains(t, ordinary, clioutput.ResourceSummaryHint)
}
