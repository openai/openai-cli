package app

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestModelsListPrintKeepsCompleteRecords(t *testing.T) {
	var output bytes.Buffer
	opts := ShowJSONOpts{Context: t.Context(), Stdout: &output,
		Operation: "(resource) models > (method) list", OutputKind: OutputPageItem}
	items := []gjson.Result{
		gjson.Parse(`{"id":"model-a","object":"model","created":123,"owned_by":"openai"}`),
		gjson.Parse(`{"id":"model-b","object":"model","created":456,"owned_by":"owner-\u001b[31m"}`),
	}
	model := loadedListModel(opts, items, false, 80, 1)
	_, quit := model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	require.NotNil(t, quit)
	require.NoError(t, model.Finish(nil, tea.ErrProgramKilled))
	require.Equal(t, "ID: model-a\nObject: model\nCreated: 123\nOwned by: openai\n\n"+
		"ID: model-b\nObject: model\nCreated: 456\nOwned by: owner-\\u001b[31m\n", output.String())
	require.False(t, opts.RawOutput, "printing must not mutate ordinary output options")

	// Ordinary labeled output retains its existing summary contract.
	ordinary, err := renderListNavigationLabels(opts, items)
	require.NoError(t, err)
	require.NotContains(t, ordinary, "Created:")
	require.Contains(t, ordinary, clioutput.ResourceSummaryHint)
}
