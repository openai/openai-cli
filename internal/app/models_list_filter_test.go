package app

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelsListFilterQuotedEmptySelection(t *testing.T) {
	for _, test := range []struct{ filter, want string }{
		{`id=""`, ""},
		{`id=''`, ""},
		{`id~""`, "review-a\nreview-b\n"},
		{`id~''`, "review-a\nreview-b\n"},
	} {
		source, _ := staticModelIterator(t, `[{"object":"model","id":"review-b"},{"object":"model","id":"review-a"}]`, -1, nil)
		selectionContext := modelSelectionContext(t, test.filter, true, "id")
		var output bytes.Buffer
		opts := ShowJSONOpts{Context: selectionContext,
			Operation: "(resource) models > (method) list", OutputKind: OutputPageItem,
			Format: "jsonl", ExplicitFormat: true, Transform: "id", RawOutput: true, Stdout: &output}
		require.NoError(t, ShowJSONIterator(source, -1, opts))
		require.Equal(t, test.want, output.String())
		require.Equal(t, 2, source.Index())
	}
}

func TestModelsListFilterKeepsNumericIDsDistinct(t *testing.T) {
	for _, filter := range []string{`id=001`, `id="001"`} {
		source, _ := staticModelIterator(t, `[{"object":"model","id":"1"},{"object":"model","id":"001"}]`, -1, nil)
		selectionContext := modelSelectionContext(t, filter, true, "id")
		var output bytes.Buffer
		opts := ShowJSONOpts{Context: selectionContext,
			Operation: "(resource) models > (method) list", OutputKind: OutputPageItem,
			Format: "jsonl", ExplicitFormat: true, Transform: "id", RawOutput: true, Stdout: &output}
		require.NoError(t, ShowJSONIterator(source, -1, opts))
		require.Equal(t, "001\n", output.String())
	}
}
