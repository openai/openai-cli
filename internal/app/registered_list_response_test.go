package app

import (
	"context"
	"testing"

	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func collectedResponse(t *testing.T, operation, raw string) listview.Page {
	t.Helper()
	var result listview.Page
	for page, err := range new(registeredList).pages(t.Context(), func(ctx context.Context) error {
		return ShowJSON(gjson.Parse(raw), ShowJSONOpts{Context: ctx, Operation: operation, OutputKind: OutputResponse})
	}) {
		require.NoError(t, err)
		result = page
	}
	return result
}

func TestRegisteredWebhookEventCatalog(t *testing.T) {
	const operation = "(resource) webhooks.event_types > (method) list"
	raw := `{"object":"list","data":["fine_tuning.future.event","response.completed","future.\"quoted\""]}`
	page := collectedResponse(t, operation, raw)
	require.Len(t, page.Items, 2)
	require.Equal(t, "response.completed", page.Items[0].Get("event").Str)
	require.Equal(t, "background_responses", page.Items[0].Get("category").Str)
	require.Equal(t, `future."quoted"`, page.Items[1].Get("event").Str)
	require.Equal(t, "other", page.Items[1].Get("category").Str)

	for input, notice := range map[string]string{
		`{"object":"list","data":[]}`:                           "No webhook events are available for this project.",
		`{"object":"list","data":["fine_tuning.future.event"]}`: "The available webhook events are not supported by the CLI.",
	} {
		page := collectedResponse(t, operation, input)
		require.Empty(t, page.Items)
		require.Equal(t, notice, page.Notice)
	}
	for _, input := range []string{
		`{"object":"future_catalog","data":["response.completed"]}`,
		`{"object":"list","data":["response.completed"],"data":[]}`,
		`{"object":"list","data":[{"event":"future.event","detail":"keep"}]}`,
	} {
		page := collectedResponse(t, operation, input)
		require.True(t, page.FullLabels)
		require.Len(t, page.Items, 1)
		require.Equal(t, input, page.Items[0].Raw)
		require.Contains(t, page.Notice, "unexpected event catalog")
	}
}

func TestRegisteredSingleResponseUnwrapsOnlyCompleteLists(t *testing.T) {
	const op = "(resource) widgets > (method) list"
	for _, raw := range []string{
		`{"object":"future","data":[]}`,
		`{"object":"list","data":[],"first_id":{"detail":"keep"}}`,
		`{"data":[],"has_more":true}`,
		`{"data":[],"extra":42}`,
		`{"data":[1],"data":[2]}`,
	} {
		page := collectedResponse(t, op, raw)
		require.True(t, page.FullLabels, raw)
		require.Len(t, page.Items, 1)
		require.Equal(t, raw, page.Items[0].Raw)
	}
	for _, raw := range []string{
		`[1,2]`, `{"data":[1,2]}`, `{"object":"list","has_more":false,"data":[1,2]}`,
	} {
		page := collectedResponse(t, op, raw)
		require.Len(t, page.Items, 2)
		require.Equal(t, int64(2), page.Items[1].Int())
	}
}
