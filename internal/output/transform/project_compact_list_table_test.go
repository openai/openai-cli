package transform

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompactListTableColumns(t *testing.T) {
	for _, tc := range []struct {
		resource string
		record   string
		headers  []string
		row      []string
	}{
		{"webhooks", `{"id":"whe_test","name":"My endpoint","object":"webhook_endpoint","url":"https://example.test/hook","event_types":["batch.failed","batch.completed"],"signing_secret_hint":"whsec_...demo","created_at":100,"updated_at":200}`,
			[]string{"ID", "NAME", "EVENTS"}, []string{"whe_test", "My endpoint", "batch.failed, batch.completed"}},
		{"skills", `{"id":"skill_test","object":"skill","name":"Summarize","description":"Create a summary","default_version":"1","latest_version":"2","created_at":100}`,
			[]string{"ID", "NAME", "DEFAULT VERSION", "LATEST VERSION"}, []string{"skill_test", "Summarize", "1", "2"}},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			headers, rows, supported, err := projectedListCells(t.Context(), "(resource) "+tc.resource+" > (method) list", []gjson.Result{gjson.Parse(tc.record)})
			require.NoError(t, err)
			require.True(t, supported)
			require.Equal(t, tc.headers, headers)
			require.Equal(t, [][]string{tc.row}, rows)
		})
	}
}
